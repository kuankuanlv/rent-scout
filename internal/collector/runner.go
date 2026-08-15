package collector

import (
	"context"
	"errors"
	"fmt"
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
		enabled: enabled, manual: manual}
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

// SourceEnabled 源当前启用态（SourceController 接口实现）
func (r *Runner) SourceEnabled(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled[name]
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

// runSource 单源循环：停用判定 → 手动触发/定时轮次（失败退避 + jitter 抖动）。
// 停用态不跑轮次，仅响应手动触发（规格 7.1 手动触发抓取）与 ctx 取消；
// 周期轮询恢复判定——enable 后无需额外信号，循环自然恢复
func (r *Runner) runSource(ctx context.Context, src Source) {
	log := pkglog.Component(pkglog.SourceCollector(src.Name()))
	failStreak := 0
	prevEnabled := true
	for {
		cfg := r.rt.Get()
		interval := time.Duration(cfg.Collector.SourceInterval(src.Name())) * time.Second
		jitter := cfg.Collector.JitterRatio
		if !r.SourceEnabled(src.Name()) {
			// 仅状态迁移时打一次日志，避免 1s 轮询刷屏
			if prevEnabled {
				log.Info("源已暂停", "source", src.Name())
			}
			prevEnabled = false
			select {
			case <-ctx.Done():
				return
			case <-r.manual[src.Name()]:
				// 手动触发：即使停用也跑一轮（规格 7.1 手动触发抓取）
				if _, err := r.runSourceOnce(ctx, src, r.trigger); err != nil {
					log.Warn("手动触发失败", "err", err)
				} else {
					failStreak = 0
				}
			case <-time.After(time.Second):
				// 周期轮询：仅用于恢复判定，不执行轮次
			}
			continue
		}
		prevEnabled = true
		t0 := time.Now()
		res, err := r.runSourceOnce(ctx, src, r.trigger)
		if err != nil {
			failStreak++
			wait := time.Duration(1<<min(failStreak-1, 5)) * time.Minute
			log.Warn("本轮失败，等待下一轮", "err", err, "耗时", time.Since(t0).Round(time.Millisecond).String(),
				"wait_s", int(wait.Seconds()), "attempt", failStreak)
			if !waitRound(ctx, r.manual[src.Name()], wait) {
				return
			}
			continue
		}
		failStreak = 0
		wait := jittered(interval, jitter)
		log.Info("本轮结束，等待下一轮",
			"耗时", time.Since(t0).Round(time.Millisecond).String(),
			"新帖", res.NewPosts,
			"offset", emptyDash(res.Offset),
			"seen_newest", formatWatermark(res.SeenNewest),
			"pages", res.Pages,
			"wait_s", int(wait.Seconds()),
		)
		if !waitRound(ctx, r.manual[src.Name()], wait) {
			return
		}
	}
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
	NewPosts   int
	Offset     string
	SeenNewest string
	Pages      int
}

// RunOnce 跑一轮采集（测试与手动触发共用）
func (r *Runner) RunOnce(ctx context.Context, src Source, trigger chan<- struct{}) error {
	_, err := r.runSourceOnce(ctx, src, trigger)
	return err
}

const maxPagesPerRound = 10

// runSourceOnce 单轮采集：最多翻 10 页，进度写回 offset（page + seen_newest）
func (r *Runner) runSourceOnce(ctx context.Context, src Source, trigger chan<- struct{}) (roundResult, error) {
	log := pkglog.Component(pkglog.SourceCollector(src.Name()))
	cfg := r.rt.Get()
	now := time.Now()
	start, end, err := sourceTimeWindow(src.Name(), cfg, now)
	if err != nil {
		return roundResult{}, err
	}

	prog, _, err := r.store.GetProgress(src.Name())
	if err != nil {
		return roundResult{}, err
	}
	fp := sourceFingerprint(src.Name(), cfg)
	if prog.Fingerprint != "" && prog.Fingerprint != fp {
		log.Info("源配置变了，重置采集进度", "source", src.Name(), "old", prog.Fingerprint, "new", fp)
		prog = store.SourceProgress{}
	}
	prog.Fingerprint = fp

	catchUp := prog.CatchingUp()
	listCursor := ""
	if !catchUp {
		listCursor = prog.Page
	}
	log.Info("开始本轮采集",
		"offset", emptyDash(listCursor),
		"seen_newest", formatWatermark(prog.SeenNewest),
		"窗从", start.Format("01-02 15:04"),
		"窗至", end.Format("01-02 15:04"),
	)

	var wm time.Time
	if prog.SeenNewest != "" {
		wm = parseWatermark(prog.SeenNewest)
	}
	persist := func() error {
		prog.Fingerprint = fp
		if !wm.IsZero() {
			prog.SeenNewest = wm.Format(time.RFC3339Nano)
		}
		return r.store.SetProgress(src.Name(), prog)
	}
	newPosts := 0
	pages := 0
	firstHTTP := true
	pace := func() {
		if firstHTTP {
			firstHTTP = false
			return
		}
		if gap := r.requestGap(src); gap > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(gap):
			}
		}
	}
	out := func() roundResult {
		return roundResult{NewPosts: newPosts, Offset: prog.Page, SeenNewest: prog.SeenNewest, Pages: pages}
	}
	for pages < maxPagesPerRound {
		pace()
		items, next, err := src.List(ctx, listCursor)
		if err != nil {
			if cookieDead(err) {
				log.Error("cookie 失效，本轮结束", "err", err)
				return out(), nil
			}
			return roundResult{}, fmt.Errorf("列表页: %w", err)
		}
		pages++
		var fresh []ListItem
		stop := false
		for _, it := range items {
			if catchUp && !wm.IsZero() && !it.PublishedAt.After(wm) {
				stop = true
				break
			}
			if it.PublishedAt.Before(start) {
				stop = true
				break
			}
			if it.PublishedAt.After(end) {
				continue
			}
			fresh = append(fresh, it)
			if it.PublishedAt.After(wm) {
				wm = it.PublishedAt
			}
		}
		if len(fresh) == 0 {
			if stop {
				if ng := skipGroup(src, listCursor); ng != "" {
					if !catchUp {
						prog.Page = ng
						if err := persist(); err != nil {
							return roundResult{}, err
						}
						break
					}
					listCursor = ng
					if err := persist(); err != nil {
						return roundResult{}, err
					}
					continue
				}
				prog = sealProgress(prog, wm, end)
				if err := persist(); err != nil {
					return roundResult{}, err
				}
				break
			}
			if next == "" {
				prog = sealProgress(prog, wm, end)
				if err := persist(); err != nil {
					return roundResult{}, err
				}
				break
			}
			if !catchUp {
				prog.Page = next
				if err := persist(); err != nil {
					return roundResult{}, err
				}
				break
			}
			listCursor = next
			continue
		}
		ids := make([]string, 0, len(fresh))
		for _, it := range fresh {
			ids = append(ids, it.ExternalID)
		}
		existing, err := r.store.ExistsByExternalIDs(src.Name(), ids)
		if err != nil {
			return roundResult{}, err
		}
		for _, it := range fresh {
			if existing[it.ExternalID] {
				continue
			}
			pace()
			post, err := src.Detail(ctx, it)
			if err != nil {
				if cookieDead(err) {
					log.Error("cookie 失效，本轮结束", "id", it.ExternalID, "err", err)
					_ = persist()
					return out(), nil
				}
				log.Warn("详情拉取失败已跳过", "id", it.ExternalID, "err", err)
				continue
			}
			if post.CollectedAt.IsZero() {
				post.CollectedAt = time.Now()
			}
			added, err := r.store.InsertPost(post)
			if err != nil {
				return roundResult{}, fmt.Errorf("入库: %w", err)
			}
			if added {
				newPosts++
			}
		}

		if stop {
			if ng := skipGroup(src, listCursor); ng != "" {
				if !catchUp {
					prog.Page = ng
					if err := persist(); err != nil {
						return roundResult{}, err
					}
					break
				}
				listCursor = ng
				if err := persist(); err != nil {
					return roundResult{}, err
				}
				continue
			}
			prog = sealProgress(prog, wm, end)
			if err := persist(); err != nil {
				return roundResult{}, err
			}
			break
		}
		if next == "" {
			prog = sealProgress(prog, wm, end)
			if err := persist(); err != nil {
				return roundResult{}, err
			}
			break
		}
		listCursor = next
		if !catchUp {
			prog.Page = next
		}
		if err := persist(); err != nil {
			return roundResult{}, err
		}
	}
	if newPosts > 0 && trigger != nil {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	return out(), nil
}

func sealProgress(p store.SourceProgress, wm, end time.Time) store.SourceProgress {
	p.Page = ""
	if !wm.IsZero() {
		p.SeenNewest = wm.Format(time.RFC3339Nano)
	} else if p.SeenNewest == "" {
		p.SeenNewest = end.Format(time.RFC3339Nano)
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
		return name + "|" + cfg.Collector.Douban.RangeFrom + "|" + strings.Join(cfg.Collector.Douban.Groups, ",")
	}
	return name + "|from=" + strconv.Itoa(cfg.Collector.MaxAgeDays)
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
	if src.Name() != models.SourceDouban.String() {
		return 0
	}
	n := r.rt.Get().Collector.Douban.Interval
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

func formatWatermark(s string) string {
	t := parseWatermark(s)
	if t.IsZero() {
		return emptyDash(s)
	}
	return t.Format("01-02 15:04")
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
