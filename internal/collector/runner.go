package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Runner 采集调度：每源独立 goroutine（规格 4.5，源间并发）。
// 单轮流程（调整规格 E）：List → 时间窗过滤（超窗停页）→ 批量查重
// → 仅新帖 Detail → 入库 → 游标 → 触发信号；轮间间隔 ±jitter 抖动，
// 失败指数退避（防检测，规格 4.5）。
// 控制面（规格 7.1）：enabled 启停 + manual 手动触发（容量 1 非阻塞），
// 由 /api/sources 经 SourceController 接口驱动
type Runner struct {
	rt      *config.HotConfig
	store   *store.Store
	sources []Source
	trigger chan<- struct{}
	mu      sync.Mutex
	enabled map[string]bool
	manual  map[string]chan struct{}
	roundNo map[string]int // 进程内按源累加，重启从 1
}

// NewRunner 创建调度器
func NewRunner(rt *config.HotConfig, st *store.Store, sources []Source, trigger chan<- struct{}) *Runner {
	enabled := make(map[string]bool, len(sources))
	manual := make(map[string]chan struct{}, len(sources))
	for _, src := range sources {
		enabled[src.Name()] = true
		manual[src.Name()] = make(chan struct{}, 1)
	}
	return &Runner{rt: rt, store: st, sources: sources, trigger: trigger,
		enabled: enabled, manual: manual, roundNo: make(map[string]int, len(sources))}
}

// Run 启动全部源的独立 goroutine（源间并发，互不阻塞）；ctx 取消即全部停止
func (r *Runner) Run(ctx context.Context) {
	for _, src := range r.sources {
		go r.runSource(ctx, src)
	}
}

// Sources 源名列表（SourceController 接口实现）
func (r *Runner) Sources() []string {
	names := make([]string, 0, len(r.sources))
	for _, src := range r.sources {
		names = append(names, src.Name())
	}
	return names
}

// SetEnabled 启停源（SourceController 接口实现）：enabled[name]=on；
// 启用时无需额外信号——runSource 停用态周期轮询，循环自然恢复；未知源返回错误
func (r *Runner) SetEnabled(name string, on bool) error {
	if !r.hasSource(name) {
		return fmt.Errorf("未知源 %s", name)
	}
	r.mu.Lock()
	r.enabled[name] = on
	r.mu.Unlock()
	pkglog.Component(pkglog.SourceCollector(name)).Info("源启停切换", "source", name, "enabled", on)
	return nil
}

// Trigger 手动触发一轮（SourceController 接口实现）：非阻塞发信号
// （满则丢——已有轮次在跑或信号在途）；未知源返回错误
func (r *Runner) Trigger(name string) error {
	if !r.hasSource(name) {
		return fmt.Errorf("未知源 %s", name)
	}
	select {
	case r.manual[name] <- struct{}{}:
	default: // 满则丢：已有轮次在跑
	}
	return nil
}

func (r *Runner) configEnabled(name string) bool {
	if r.rt == nil {
		return true
	}
	app := r.rt.Get()
	if app == nil {
		return false
	}
	for _, s := range app.Collector.Sources {
		if s == name {
			return true
		}
	}
	return false
}

// SourceEnabled 源当前启用态：热配置勾选 ∩ 管理台内存开关（默认开）
func (r *Runner) SourceEnabled(name string) bool {
	if !r.configEnabled(name) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	on, ok := r.enabled[name]
	if !ok {
		return true
	}
	return on
}

// hasSource 源名是否在调度清单内
func (r *Runner) hasSource(name string) bool {
	for _, src := range r.sources {
		if src.Name() == name {
			return true
		}
	}
	return false
}

// runSource 单源循环：编排「启用检查 → 采集轮次 → 等待下一轮」，细节见各 helper
func (r *Runner) runSource(ctx context.Context, src Source) {
	log := pkglog.Component(pkglog.SourceCollector(src.Name()))
	log.Info("采集协程已启动", "source", src.Name())
	failStreak := 0
	for {
		cfg := r.rt.Get()
		if !r.SourceEnabled(src.Name()) {
			if !r.waitWhileDisabled(ctx, src, cfg, log) {
				return
			}
			continue
		}
		t0 := time.Now()
		_, err := r.runSourceOnce(ctx, src, r.trigger)
		var ok bool
		failStreak, ok = r.waitAfterRound(ctx, src, err, t0, cfg, failStreak, log)
		if !ok {
			return
		}
	}
}

// waitWhileDisabled 源未启用时的待机：周期轮询恢复判定，仅响应手动触发与 ctx 取消
func (r *Runner) waitWhileDisabled(ctx context.Context, src Source, cfg *config.AppConfig, log *slog.Logger) bool {
	prevEnabled := true
	for {
		if r.SourceEnabled(src.Name()) {
			return true
		}
		if r.configEnabled(src.Name()) {
			if prevEnabled {
				log.Info("源已暂停", "source", src.Name())
			}
		} else {
			log.Info("当前配置采集源未启用，无需执行", "source", src.Name())
		}
		prevEnabled = false
		wait := time.Duration(cfg.Collector.SourceInterval(src.Name())) * time.Second
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		if wait <= 0 {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return false
		case <-r.manual[src.Name()]:
			if _, err := r.runSourceOnce(ctx, src, r.trigger); err != nil {
				log.Warn("手动触发失败", "err", err)
			}
		case <-time.After(wait):
		}
	}
}

// waitAfterRound 一轮采集后的等待：失败指数退避，成功按配置间隔+抖动；返回下一轮 failStreak 与是否继续
func (r *Runner) waitAfterRound(ctx context.Context, src Source, err error, t0 time.Time, cfg *config.AppConfig, failStreak int, log *slog.Logger) (int, bool) {
	if err != nil {
		failStreak++
		wait := time.Duration(1<<min(failStreak-1, 5)) * time.Minute
		log.Warn("本轮失败，等待下一轮", "err", err, "耗时", time.Since(t0).Round(time.Millisecond).String(),
			"wait_s", int(wait.Seconds()), "attempt", failStreak)
		return failStreak, waitRound(ctx, r.manual[src.Name()], wait)
	}
	interval := time.Duration(cfg.Collector.SourceInterval(src.Name())) * time.Second
	wait := jittered(interval, cfg.Collector.JitterRatio)
	log.Info("等待下一轮", "wait_s", int(wait.Seconds()))
	return 0, waitRound(ctx, r.manual[src.Name()], wait)
}

func waitRound(ctx context.Context, manual <-chan struct{}, wait time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-manual:
		return true
	case <-time.After(wait):
		return true
	}
}

type roundResult struct {
	Round      int
	NewPosts   int
	ListCount  int
	Fetched    []string
	NextPos    string
	SeenNewest string
	WindowFrom string
	WindowTo   string
}

// RunOnce 跑一轮采集（测试与手动触发共用）
func (r *Runner) RunOnce(ctx context.Context, src Source, trigger chan<- struct{}) error {
	_, err := r.runSourceOnce(ctx, src, trigger)
	return err
}

const maxPagesPerGroup = 10  // 单个搜索/小组本轮最多翻这么多页，后面页更旧就该换组
const maxPagesPerRound = 200 // 整轮总页数上限，防止死循环；13 个搜索各 1 页也够
const listPageSize = 25      // 和豆瓣小组讨论列表每页条数一致

// runSourceOnce 单轮采集：每个搜索都从第1页看起，水位只决定本组要不要继续翻页
func (r *Runner) runSourceOnce(ctx context.Context, src Source, trigger chan<- struct{}) (roundResult, error) {
	log := pkglog.Component(pkglog.SourceCollector(src.Name()))
	cfg := r.rt.Get()
	now := time.Now()

	// 1. 初始化参数与状态
	start, end, err := sourceTimeWindow(src.Name(), cfg, now)
	if err != nil {
		return roundResult{}, err
	}
	prog, _, err := r.store.GetProgress(src.Name())
	if err != nil {
		return roundResult{}, err
	}
	fp := sourceFingerprint(src.Name(), cfg)
	if prog.Fingerprint != "" && fingerprintIdentity(prog.Fingerprint) != fingerprintIdentity(fp) {
		log.Info("时间窗或目标清单变了，重置采集进度", "source", src.Name(), "old", prog.Fingerprint, "new", fp)
		prog = store.SourceProgress{}
	}
	prog.Fingerprint = fp
	catchUp := prog.CatchingUp()
	listCursor := ""
	winFrom := start.Format("01-02 15:04")
	winTo := end.Format("01-02 15:04")
	round := r.nextRound(src.Name())
	log.Info(fmt.Sprintf("============ %s 第%d轮开始 %s ============", src.Name(), round, roundMode(catchUp)))
	log.Info(fmt.Sprintf("【%s 第%d轮 时间窗=%s~%s 水位=%s %s】",
		src.Name(), round, winFrom, winTo, formatWatermark(prog.SeenNewest), formatRoundScope(src)))

	// 2. 状态上下文
	wms := store.DecodeWatermarks(prog.SeenNewest)
	var wm time.Time
	wmKey := ""
	wmOrdered := false
	loadGroupWM := func(cursor string) {
		wmKey, wmOrdered = watermarkMeta(src, cursor)
		if !wmOrdered || wmKey == "" {
			wm = time.Time{}
			return
		}
		wm = store.LookupWatermark(wms, wmKey)
	}
	loadGroupWM(listCursor)

	newPosts, listCount, pages := 0, 0, 0
	fetched := make([]string, 0, maxPagesPerRound)
	firstHTTP := true
	mode := roundMode(catchUp)

	// 3. 闭包 Helper：写进度、组切换/封口（next 空串=整轮结束）、请求节流
	persist := func() error {
		prog.Fingerprint = fp
		prog.SeenNewest = store.EncodeWatermarks(wms)
		return r.store.SetProgress(src.Name(), prog)
	}
	// advanceCursor 把游标推进到 next：空串表示无更多页 → 封口收尾；
	// 否则更新进度游标继续翻。返回 finished=true 时调用方应结束本轮
	advanceCursor := func(next, reason string, idle bool) (bool, error) {
		g1, _ := parsePageCursor(listCursor)
		g2, _ := parsePageCursor(next)
		if idle && (next == "" || g1 != g2) {
			log.Info(fmt.Sprintf("【%s 第%d轮 %s %s，未收集到任何新帖】", src.Name(), round, describeCursor(src, listCursor), reason))
		}
		if m := nextGroupMsg(src.Name(), round, listCursor, next, func(c string) string { return describeCursor(src, c) }); m != "" {
			log.Info(m)
		}
		if next == "" {
			prog = sealProgress(prog, wms)
			return true, persist()
		}
		if !catchUp {
			prog.Page = next
		}
		listCursor = next
		return false, persist()
	}
	pace := func() {
		if firstHTTP {
			firstHTTP = false
			return
		}
		var gap time.Duration
		if p, ok := src.(SourcePolicy); ok {
			gap = p.RequestGap(cfg)
		}
		if gap > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(gap):
			}
		}
	}
	out := func() roundResult {
		return roundResult{
			Round: round, NewPosts: newPosts, ListCount: listCount,
			Fetched: append([]string(nil), fetched...),
			NextPos: formatNextPos(prog), SeenNewest: prog.SeenNewest,
			WindowFrom: winFrom, WindowTo: winTo,
		}
	}
	endRound := func() {
		log.Info(fmt.Sprintf("============ %s 第%d轮结束 共%d页 列表%d条 新帖%d条 下次=%s ============",
			src.Name(), round, len(fetched), listCount, newPosts, formatNextPos(prog)))
	}
	activeG, groupNew, pagesInGroup := 0, 0, 0
	type numbered struct {
		idx int
		it  ListItem
	}

	for pages < maxPagesPerRound {
		// 组切换：新组重置组内计数与水位
		g, _ := parsePageCursor(listCursor)
		if pages == 0 || g != activeG {
			activeG = g
			groupNew = 0
			pagesInGroup = 0
			loadGroupWM(listCursor)
			log.Info(fmt.Sprintf("【%s 第%d轮 开始%s】", src.Name(), round, describeCursor(src, listCursor)))
		}
		// 本组页数用尽：换下一组，或收尾结束
		if pagesInGroup >= maxPagesPerGroup {
			ng := skipGroup(src, listCursor)
			if ng == "" {
				finished, err := advanceCursor("", "本组页数用尽", groupNew == 0)
				if err != nil {
					return roundResult{}, err
				}
				if finished {
					break
				}
			}
			finished, err := advanceCursor(ng, "本组页数用尽", groupNew == 0)
			if err != nil {
				return roundResult{}, err
			}
			if finished {
				break
			}
			continue
		}
		pace()
		pageLabel := describeCursor(src, listCursor)
		items, next, err := listItems(ctx, src, listCursor, start, end)
		if err != nil {
			if cookieDead(err) {
				log.Error("cookie 失效，本轮结束", "err", err)
				endRound()
				return out(), nil
			}
			return roundResult{}, fmt.Errorf("列表页: %w", err)
		}
		pages++
		pagesInGroup++
		listCount += len(items)
		fetched = append(fetched, pageLabel)
		log.Info(fmt.Sprintf("【%s 第%d轮 %s %s 本页%d条】",
			src.Name(), round, mode, pageLabel, len(items)))
		var fresh []numbered
		stop := false
		hitWm, hitOld := false, false
		tooNew := 0
		for i, it := range items {
			if wmOrdered && catchUp && !wm.IsZero() && !it.PublishedAt.After(wm) {
				stop = true
				hitWm = true
				break
			}
			if it.PublishedAt.Before(start) {
				stop = true
				hitOld = true
				break
			}
			if it.PublishedAt.After(end) {
				tooNew++
				continue
			}
			fresh = append(fresh, numbered{idx: i + 1, it: it})
			if wmOrdered && wmKey != "" && it.PublishedAt.After(wm) {
				wm = it.PublishedAt
				wms[wmKey] = wm.Format(time.RFC3339Nano)
			}
		}
		if len(fresh) == 0 {
			if skip := formatSkipSummary(0, tooNew, hitWm, hitOld); skip != "" {
				log.Info(fmt.Sprintf("【%s 第%d轮 %s 跳过 %s】", src.Name(), round, pageLabel, skip))
			}
			idleReason := "本页无帖"
			if hitWm {
				idleReason = "到水位线"
			} else if hitOld {
				idleReason = "超出时间窗"
			}
			if stop {
				if hitOld {
					log.Info(fmt.Sprintf("【%s 第%d轮 %s 已超出时间窗，本搜索后续页更旧，不再翻页】", src.Name(), round, pageLabel))
				}
				// 无更多页则收尾结束，否则换下一组
				finished, err := advanceCursor(skipGroup(src, listCursor), idleReason, groupNew == 0)
				if err != nil {
					return roundResult{}, err
				}
				if finished {
					break
				}
				continue
			}
			// 空页：游标有下一页则继续翻，否则本轮收尾
			finished, err := advanceCursor(next, idleReason, groupNew == 0)
			if err != nil {
				return roundResult{}, err
			}
			if finished {
				break
			}
			continue
		}
		ids := make([]string, 0, len(fresh))
		for _, n := range fresh {
			ids = append(ids, n.it.ExternalID)
		}
		existing, err := r.store.ExistsByExternalIDs(src.Name(), ids)
		if err != nil {
			return roundResult{}, err
		}
		existN := 0
		for _, n := range fresh {
			it := n.it
			if existing[it.ExternalID] {
				existN++
				continue
			}
			pace()
			post, err := src.Detail(ctx, it)
			if err != nil {
				if cookieDead(err) {
					log.Error("cookie 失效，本轮结束", "id", it.ExternalID, "err", err)
					_ = persist()
					endRound()
					return out(), nil
				}
				log.Warn("详情拉取失败已跳过", "id", it.ExternalID, "err", err)
				continue
			}
			log.Info(fmt.Sprintf("【%s 第%d轮 %s 第%d条 新帖 %s】",
				src.Name(), round, pageLabel, n.idx, strings.TrimSpace(it.Title)))
			if post.CollectedAt.IsZero() {
				post.CollectedAt = time.Now()
			}
			added, err := r.store.InsertPost(post)
			if err != nil {
				return roundResult{}, fmt.Errorf("入库: %w", err)
			}
			if added {
				newPosts++
				groupNew++
			}
		}
		if skip := formatSkipSummary(existN, tooNew, hitWm, hitOld); skip != "" {
			log.Info(fmt.Sprintf("【%s 第%d轮 %s 跳过 %s】", src.Name(), round, pageLabel, skip))
		}

		if stop {
			idleReason := "到水位线"
			if hitOld {
				idleReason = "超出时间窗"
				log.Info(fmt.Sprintf("【%s 第%d轮 %s 已超出时间窗，本搜索后续页更旧，不再翻页】", src.Name(), round, pageLabel))
			}
			finished, err := advanceCursor(skipGroup(src, listCursor), idleReason, groupNew == 0)
			if err != nil {
				return roundResult{}, err
			}
			if finished {
				break
			}
			continue
		}
		finished, err := advanceCursor(next, "", groupNew == 0)
		if err != nil {
			return roundResult{}, err
		}
		if finished {
			break
		}
	}
	if newPosts > 0 && trigger != nil {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	endRound()
	return out(), nil
}

func sealProgress(p store.SourceProgress, wms map[string]string) store.SourceProgress {
	p.Page = ""
	if len(wms) > 0 {
		p.SeenNewest = store.EncodeWatermarks(wms)
	}
	return p
}

func parseWatermark(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func sourceFingerprint(name string, cfg *config.AppConfig) string {
	if cfg == nil {
		return name
	}
	if name == models.SourceDouban.String() {
		return name + "|" + cfg.Collector.Douban.RangeFrom + "|" + hashLines(config.HTTPURLs(cfg.Collector.Douban.Groups))
	}
	if name == models.SourceWeibo.String() {
		return name + "|" + cfg.Collector.Weibo.RangeFrom + "|" + hashLines(weiboTargetKeys(cfg))
	}
	return name + "|from=" + strconv.Itoa(cfg.Collector.MaxAgeDays)
}

func weiboTargetKeys(cfg *config.AppConfig) []string {
	var keys []string
	for _, id := range config.WeiboContainerIDs(cfg.Collector.Weibo.SuperTopics) {
		keys = append(keys, "super:"+id)
	}
	for _, id := range config.WeiboUIDs(cfg.Collector.Weibo.Users) {
		keys = append(keys, "user:"+id)
	}
	return keys
}

func hashLines(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// fingerprintIdentity 比时间窗和目标清单；旧指纹第三段若是 URL 则忽略
func fingerprintIdentity(fp string) string {
	fp = strings.TrimSpace(fp)
	parts := strings.Split(fp, "|")
	if len(parts) < 2 {
		return fp
	}
	id := parts[0] + "|" + parts[1]
	if len(parts) >= 3 {
		third := parts[2]
		if strings.Contains(third, "://") || strings.HasPrefix(third, "http") {
			return id
		}
		id += "|" + third
	}
	return id
}

// sourceTimeWindow 起点用相对天数（如 -10）；终点永远是 now，不进指纹
func sourceTimeWindow(name string, cfg *config.AppConfig, now time.Time) (time.Time, time.Time, error) {
	if name == models.SourceDouban.String() {
		start, end, err := config.ResolveTimeRange(cfg.Collector.Douban.RangeFrom, "now", now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("豆瓣拉取范围: %w", err)
		}
		return start, end, nil
	}
	if name == models.SourceWeibo.String() {
		start, end, err := config.ResolveTimeRange(cfg.Collector.Weibo.RangeFrom, "now", now)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("微博拉取范围: %w", err)
		}
		return start, end, nil
	}
	maxAge := cfg.Collector.MaxAgeDays
	if maxAge <= 0 {
		maxAge = 7
	}
	return now.Add(-time.Duration(maxAge) * 24 * time.Hour), now, nil
}

// jittered 间隔随机抖动：interval * (1 ± jitter)
func jittered(interval time.Duration, jitter float64) time.Duration {
	if jitter <= 0 {
		return interval
	}
	ratio := 1 + (rand.Float64()*2-1)*jitter
	return time.Duration(float64(interval) * ratio)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cookieDead(err error) bool {
	return errors.Is(err, cookie.ErrCookieInvalid) || errors.Is(err, cookie.ErrCookieMissing)
}

func (r *Runner) requestGap(src Source) time.Duration {
	if r.rt == nil {
		return 0
	}
	cfg := r.rt.Get()
	if cfg == nil {
		return 0
	}
	n := 0
	switch src.Name() {
	case models.SourceDouban.String():
		n = cfg.Collector.Douban.Interval
	case models.SourceWeibo.String():
		n = cfg.Collector.Weibo.Interval
	}
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func listItems(ctx context.Context, src Source, cursor string, start, end time.Time) ([]ListItem, string, error) {
	if w, ok := src.(TimeWindowLister); ok {
		return w.ListInWindow(ctx, cursor, start, end)
	}
	return src.List(ctx, cursor)
}

func formatWatermark(s string) string {
	m := store.DecodeWatermarks(s)
	if len(m) == 0 {
		if strings.TrimSpace(s) == "" {
			return "无"
		}
		return s
	}
	if len(m) == 1 {
		for _, v := range m {
			t := parseWatermark(v)
			if t.IsZero() {
				return v
			}
			return t.Format("01-02 15:04")
		}
	}
	return fmt.Sprintf("%d个目标", len(m))
}

// formatPageCursor 豆瓣游标转成人话：组从 1 数，页=offset/25+1
func formatPageCursor(cursor string) string {
	g, p := parsePageCursor(cursor)
	return fmt.Sprintf("组%d第%d页", g, p)
}

func parsePageCursor(cursor string) (group1, page int) {
	gi, off := 0, 0
	c := strings.TrimSpace(cursor)
	if c != "" {
		parts := strings.SplitN(c, ":", 2)
		gi, _ = strconv.Atoi(parts[0])
		if len(parts) == 2 {
			off, _ = strconv.Atoi(parts[1])
		}
	}
	if off < 0 {
		off = 0
	}
	return gi + 1, off/listPageSize + 1
}

func roundMode(catchUp bool) string {
	if catchUp {
		return "追新"
	}
	return "翻历史"
}

func (r *Runner) nextRound(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roundNo == nil {
		r.roundNo = map[string]int{}
	}
	r.roundNo[name]++
	return r.roundNo[name]
}

func describeCursor(src Source, cursor string) string {
	if d, ok := src.(CursorDescriber); ok {
		if s := strings.TrimSpace(d.DescribeCursor(cursor)); s != "" {
			return s
		}
	}
	return formatPageCursor(cursor)
}

func stripPageSuffix(s string) string {
	i := strings.LastIndex(s, "第")
	if i >= 0 && strings.HasSuffix(s, "页") {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// formatRoundScope 本轮会扫的组/搜索清单
func formatRoundScope(src Source) string {
	if src == nil {
		return "范围 无"
	}
	var parts []string
	c := ""
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		g, _ := parsePageCursor(c)
		if seen[g] {
			break
		}
		seen[g] = true
		parts = append(parts, stripPageSuffix(describeCursor(src, c)))
		ng := skipGroup(src, c)
		if ng == "" {
			break
		}
		c = ng
	}
	if len(parts) == 0 {
		return "范围 无"
	}
	return fmt.Sprintf("范围共%d个 %s", len(parts), strings.Join(parts, "、"))
}

func nextGroupMsg(src string, round int, from, to string, label func(string) string) string {
	if strings.TrimSpace(to) == "" {
		return ""
	}
	g1, _ := parsePageCursor(from)
	g2, _ := parsePageCursor(to)
	if g1 == g2 {
		return ""
	}
	fl, tl := formatPageCursor(from), formatPageCursor(to)
	if label != nil {
		fl, tl = label(from), label(to)
	}
	return fmt.Sprintf("【%s 第%d轮 %s执行完成，开始%s】", src, round, fl, tl)
}

func formatSkipSummary(exist, tooNew int, hitWm, hitOld bool) string {
	var parts []string
	if exist > 0 {
		parts = append(parts, fmt.Sprintf("已存在%d", exist))
	}
	if tooNew > 0 {
		parts = append(parts, fmt.Sprintf("超窗新%d", tooNew))
	}
	if hitOld {
		parts = append(parts, "超窗旧")
	}
	if hitWm {
		parts = append(parts, "到水位线")
	}
	return strings.Join(parts, " ")
}

func formatStartPos(catchUp bool, cursor string) string {
	if catchUp {
		return "追新·" + formatPageCursor("")
	}
	return "翻历史·" + formatPageCursor(cursor)
}

func formatNextPos(p store.SourceProgress) string {
	if p.CatchingUp() {
		return "追新·" + formatPageCursor("")
	}
	return "翻历史·" + formatPageCursor(p.Page)
}
