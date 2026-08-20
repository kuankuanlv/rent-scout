package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/log"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// Runner 只管调度：开循环、落库去重、冷却；翻页细节交给 Iterator。
type Runner struct {
	rt            *config.HotConfig
	store         *store.Store
	sources       []Source
	trigger       chan<- struct{}
	mu            sync.Mutex
	enabled       map[string]bool
	// coolDownUntil Cookie 挂了之类的致命错，先歇 1 小时别狂打
	coolDownUntil map[string]time.Time
}

// NewRunner trigger 有新帖时通知下游（比如通知拉批）；可传 nil。
func NewRunner(rt *config.HotConfig, st *store.Store, sources []Source, trigger chan<- struct{}) *Runner {
	r := &Runner{
		rt:            rt,
		store:         st,
		sources:       append([]Source(nil), sources...),
		trigger:       trigger,
		enabled:       map[string]bool{},
		coolDownUntil: map[string]time.Time{},
	}
	enabledSet := map[string]bool{}
	if rt != nil {
		if app := rt.Get(); app != nil {
			for _, n := range app.Collector.Sources {
				enabledSet[n] = true
			}
		}
	}
	for _, src := range sources {
		if src == nil {
			continue
		}
		name := src.Name()
		// 协程常驻；没勾选时周期轮询跳过
		r.enabled[name] = enabledSet[name]
	}
	// 配置勾了但没挂进 sources 的，也记进 enabled，方便查状态
	for name, on := range enabledSet {
		if _, ok := r.enabled[name]; !ok {
			r.enabled[name] = on
		}
	}
	// 间隔抖动用；JitterRatio=0 时完全固定
	rand.Seed(time.Now().UnixNano())
	return r
}

// Run 每个源起一个常驻循环，直到 ctx 取消。
func (r *Runner) Run(ctx context.Context) {
	for _, src := range r.sources {
		if src == nil {
			continue
		}
		go r.runSourceLoop(ctx, src)
	}
	<-ctx.Done()
}

func (r *Runner) runSourceLoop(ctx context.Context, src Source) {
	name := src.Name()
	interval := r.roundInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !r.SourceEnabled(name) {
				continue
			}
			r.runSourceOnceWithCooldown(ctx, src, r.trigger)
		}
	}
}

func (r *Runner) roundInterval() time.Duration {
	if r.rt == nil || r.rt.Get() == nil {
		return 300 * time.Second
	}
	sec := r.rt.Get().Collector.Interval
	if sec <= 0 {
		sec = 300
	}
	d := time.Duration(sec) * time.Second
	// 随机抖一点间隔，比例 0 就不抖
	j := r.rt.Get().Collector.JitterRatio
	if j <= 0 {
		return d
	}
	// 落在 [1-j, 1+j] 倍
	f := 1 + (rand.Float64()*2-1)*j
	if f < 0.01 {
		f = 0.01
	}
	return time.Duration(float64(d) * f)
}

func (r *Runner) runSourceOnceWithCooldown(ctx context.Context, src Source, trigger chan<- struct{}) {
	name := src.Name()
	if until, ok := r.coolDownUntil[name]; ok && time.Now().Before(until) {
		return
	}
	_, err := r.runSourceOnce(ctx, src, trigger)
	if err != nil && errors.Is(err, ErrUnrecoverable) {
		log.Warn(name, "致命异常，冷却 1 小时", "err", err)
		r.coolDownUntil[name] = time.Now().Add(1 * time.Hour)
	}
}

// RunOnce 跑一轮采集。
func (r *Runner) RunOnce(ctx context.Context, src Source, trigger chan<- struct{}) error {
	_, err := r.runSourceOnce(ctx, src, trigger)
	return err
}

// runSourceOnce 读进度 → 迭代 → 落库；返回本轮有没有写进新帖。
func (r *Runner) runSourceOnce(ctx context.Context, src Source, trigger chan<- struct{}) (bool, error) {
	log.Info(src.Name(), "=== 新一轮采集开始 ===")
	prog, ok, err := r.store.GetProgress(src.Name())
	if err != nil {
		return false, err
	}

	now := time.Now()
	wantFP := r.fingerprintForSource(src)
	// 配置指纹变了（改时间窗/目标清单）就丢旧进度，从头采
	if !ok || strings.TrimSpace(prog.Fingerprint) == "" || prog.Fingerprint != wantFP {
		prog = store.SourceProgress{Fingerprint: wantFP}
	}

	start, end := r.timeWindowForSource(src.Name(), now)
	// 有水位就从列表头重跑，旧帖靠水位挡
	itStateCursor := r.startCursorForIterator(prog)
	it := src.NewIterator(itStateCursor, start, end)

	// SeenNewest：JSON 多目标水位，或单条时间戳
	watermarks := r.decodeSeenNewest(prog.SeenNewest)
	catchingUp := prog.CatchingUp()

	// 没声明 TimeOrdered 的源当有序（降序流）
	timeOrdered := true
	if to, ok := src.(interface{ TimeOrdered(string) bool }); ok {
		timeOrdered = to.TimeOrdered(itStateCursor)
	}
	wmKeyer, _ := src.(interface{ WatermarkKey(string) string })

	// 回填可翻多页；追新只打首页
	maxPages := 1
	if !catchingUp {
		maxPages = 64
	}

	var (
		wroteAny          bool
		seenNonEmptyValue bool
		lastCheckpoint    string
		currentCursor     = itStateCursor
	)

	for pages := 0; pages < maxPages && it.Next(ctx); pages++ {
		ck := strings.TrimSpace(it.Checkpoint())
		if pages > 0 && ck == lastCheckpoint {
			// checkpoint 没动，当翻完了，防死循环
			break
		}

		items := it.Value()

		// 追新：只要严格新于水位的帖
		if catchingUp && timeOrdered && len(watermarks) > 0 {
			key := "default"
			if wmKeyer != nil {
				if k := strings.TrimSpace(wmKeyer.WatermarkKey(currentCursor)); k != "" {
					key = k
				}
			}
			if wmTime := store.LookupWatermark(watermarks, key); !wmTime.IsZero() {
				filtered := items[:0]
				for _, itv := range items {
					if itv.PublishedAt.After(wmTime) {
						filtered = append(filtered, itv)
					}
				}
				items = filtered
			}
		}

		// 有序回填：见过内容后又空页，当撞线停
		if !catchingUp && timeOrdered && seenNonEmptyValue && len(items) == 0 {
			break
		}

		for _, item := range items {
			m, err := r.store.ExistsByExternalIDs(src.Name(), []string{item.ExternalID})
			if err != nil || m[item.ExternalID] {
				continue
			}

			post, err := src.Detail(ctx, item)
			if err != nil {
				continue
			}
			added, err := r.store.InsertPost(post)
			if err != nil {
				continue
			}
			if added {
				wroteAny = true
				if trigger != nil {
					select {
					case trigger <- struct{}{}:
					default:
					}
				}
			}

			// 回填时抬高水位，下一轮当停止线
			if !catchingUp && timeOrdered {
				key := "default"
				if wmKeyer != nil {
					if k := strings.TrimSpace(wmKeyer.WatermarkKey(currentCursor)); k != "" {
						key = k
					}
				}
				oldWM := store.LookupWatermark(watermarks, key)
				if oldWM.IsZero() || item.PublishedAt.After(oldWM) {
					watermarks[key] = item.PublishedAt.Format(time.RFC3339Nano)
				}
			}
		}

		if len(items) > 0 {
			seenNonEmptyValue = true
		}

		// 每页落完立刻存进度；追新阶段 Page 保持空
		prog.Fingerprint = wantFP
		if catchingUp {
			prog.Page = ""
		} else {
			prog.Page = ck
		}
		_ = r.store.SetProgress(src.Name(), prog)

		// 下一页游标空了就停
		if !catchingUp && ck == "" {
			lastCheckpoint = ck
			currentCursor = ck
			break
		}

		lastCheckpoint = ck
		currentCursor = ck
	}

	if err := it.Err(); err != nil {
		// Cookie 失效当致命错，外层会冷却
		if errors.Is(err, cookie.ErrCookieInvalid) {
			return wroteAny, fmt.Errorf("%w: %v", ErrUnrecoverable, err)
		}
		return wroteAny, err
	}

	// 本轮结束：Page 清空 + 写入水位 → 下一轮进追新
	prog.Page = ""
	prog.SeenNewest = store.EncodeWatermarks(watermarks)
	_ = r.store.SetProgress(src.Name(), prog)

	return wroteAny, nil
}

// fingerprintForSource 配置身份；变了就重置进度。源自己能算就用源的。
func (r *Runner) fingerprintForSource(src Source) string {
	if r.rt == nil || r.rt.Get() == nil {
		return src.Name()
	}
	app := r.rt.Get()
	if fp, ok := src.(interface{ Fingerprint(*config.AppConfig) string }); ok {
		return fp.Fingerprint(app)
	}
	// 微博包装源没实现 Fingerprint 时，按超话/博主清单自己算
	if src.Name() == models.SourceWeibo.String() {
		var keys []string
		for _, id := range config.WeiboContainerIDs(app.Collector.Weibo.SuperTopics) {
			keys = append(keys, "super:"+id)
		}
		for _, id := range config.WeiboUIDs(app.Collector.Weibo.Users) {
			keys = append(keys, "user:"+id)
		}
		sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
		var h [8]byte
		copy(h[:], sum[:8])
		return models.SourceWeibo.String() + "|" + app.Collector.Weibo.RangeFrom + "|" + hex.EncodeToString(h[:])
	}
	return src.Name()
}

// timeWindowForSource 绝对时间窗；豆瓣/微博走各自 Range，其它用 MaxAgeDays。
func (r *Runner) timeWindowForSource(sourceName string, now time.Time) (time.Time, time.Time) {
	if r.rt == nil || r.rt.Get() == nil {
		return now.Add(-7 * 24 * time.Hour), now
	}
	app := r.rt.Get()
	switch sourceName {
	case models.SourceDouban.String():
		start, end, err := config.ResolveTimeRange(app.Collector.Douban.RangeFrom, app.Collector.Douban.RangeTo, now)
		if err == nil {
			return start, end
		}
	case models.SourceWeibo.String():
		start, end, err := config.ResolveTimeRange(app.Collector.Weibo.RangeFrom, "now", now)
		if err == nil {
			return start, end
		}
	}
	days := app.Collector.MaxAgeDays
	if days <= 0 {
		days = 7
	}
	return now.Add(-time.Duration(days) * 24 * time.Hour), now
}

// decodeSeenNewest JSON 多目标水位；否则当单条时间戳塞进 default。
func (r *Runner) decodeSeenNewest(seen string) map[string]string {
	seen = strings.TrimSpace(seen)
	if seen == "" {
		return map[string]string{}
	}
	if strings.HasPrefix(seen, "{") {
		return store.DecodeWatermarks(seen)
	}
	return map[string]string{"default": seen}
}

// startCursorForIterator 有水位从列表头；没有才续 Page。
func (r *Runner) startCursorForIterator(prog store.SourceProgress) string {
	if strings.TrimSpace(prog.SeenNewest) != "" {
		return ""
	}
	if strings.TrimSpace(prog.Page) != "" {
		return strings.TrimSpace(prog.Page)
	}
	return ""
}

func (r *Runner) Sources() []string {
	names := make([]string, 0, len(r.sources))
	for _, src := range r.sources { names = append(names, src.Name()) }
	return names
}

func (r *Runner) SetEnabled(name string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.enabled[name]; !ok {
		return fmt.Errorf("未知源 %s", name)
	}
	r.enabled[name] = on
	return nil
}

func (r *Runner) SourceEnabled(name string) bool {
	// 配置勾选 ∩ 运行时开关，两边都开才算启用
	var cfgEnabled bool
	if r.rt != nil && r.rt.Get() != nil {
		for _, n := range r.rt.Get().Collector.Sources {
			if n == name {
				cfgEnabled = true
				break
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled[name] && cfgEnabled
}
