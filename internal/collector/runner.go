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

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/config/urls"
	"rent-scout/internal/config/window"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Runner 只管调度：开循环、落库去重、冷却；翻页细节交给 Iterator。
type Runner struct {
	rt      *config.HotConfig
	store   *store.Store
	sources []Source
	trigger chan<- struct{}
	mu      sync.Mutex
	enabled map[string]bool
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

// runSourceLoop 每个源一条常驻协程：首轮立即执行一次，之后每轮重算带抖动的间隔再等待；
// 未启用/冷却中跳过本轮；真正抓取走 runSourceOnce。
// 冷却是旁路：熔断期内跳过本轮，致命错再进冷却，不掺进 run 命名。
func (r *Runner) runSourceLoop(ctx context.Context, src Source) {
	name := src.Name()
	for {
		if !r.SourceEnabled(name) {
			// 未启用：空转一轮，等下一个间隔再查
		} else if r.inCoolDown(name) {
			// 冷却中：跳过本轮
		} else if _, err := r.runSourceOnce(ctx, src, r.trigger); err != nil && errors.Is(err, ErrUnrecoverable) {
			r.enterCoolDown(name, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.roundInterval()):
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

// inCoolDown 还在熔断歇着就不打网
func (r *Runner) inCoolDown(name string) bool {
	until, ok := r.coolDownUntil[name]
	return ok && time.Now().Before(until)
}

// enterCoolDown Cookie 挂了这类致命错：歇 1 小时再试
func (r *Runner) enterCoolDown(name string, err error) {
	pkglog.SourceWarn(name, "致命异常，冷却 1 小时", "err", err)
	r.coolDownUntil[name] = time.Now().Add(1 * time.Hour)
}

// runSourceOnce 读进度 → 开迭代器翻页 → 去重落库；返回本轮有没有写进新帖。
// 概念：fingerprint=配置身份；page/checkpoint=Iterator 黑盒进度（含水位/offset，Runner 不拆）；时间窗=本轮允许的绝对区间。
func (r *Runner) runSourceOnce(ctx context.Context, src Source, trigger chan<- struct{}) (bool, error) {
	name := src.Name()
	prog, ok, err := r.store.GetProgress(name)
	if err != nil {
		return false, err
	}

	now := time.Now()
	wantFP := r.fingerprintForSource(src)
	oldFP := strings.TrimSpace(prog.Fingerprint)
	checkpoint := strings.TrimSpace(prog.Page)
	// 配置指纹变了（改时间窗/目标清单）就丢旧进度，从头采
	fpReset := !ok || oldFP == "" || oldFP != wantFP
	if fpReset {
		prog = store.SourceProgress{Fingerprint: wantFP}
		checkpoint = ""
	}

	start, end := r.timeWindowForSource(name, now)
	pkglog.SourceInfo(name, "=== 新一轮采集开始 ===",
		"fingerprint", wantFP,
		"fp_reset", fpReset,
		"checkpoint", checkpoint,
		"window_from", start.Format(time.RFC3339),
		"window_to", end.Format(time.RFC3339),
	)
	it := src.NewIterator(checkpoint, start, end)

	var wroteAny bool
	lastCheckpoint := checkpoint
	pageNo := 0

	for it.Next(ctx) {
		pageNo++
		items := it.Value()
		var newCount, skipExist, detailFail int
		for _, item := range items {
			m, err := r.store.ExistsByExternalIDs(name, []string{item.ExternalID})
			if err != nil || m[item.ExternalID] {
				skipExist++
				continue
			}

			post, err := src.Detail(ctx, item)
			if err != nil {
				detailFail++
				continue
			}
			added, err := r.store.InsertPost(post)
			if err != nil {
				continue
			}
			if added {
				newCount++
				wroteAny = true
				if trigger != nil {
					select {
					case trigger <- struct{}{}:
					default:
					}
				}
			}
		}

		ck := strings.TrimSpace(it.Checkpoint())
		pkglog.SourceInfo(name, "翻页落库",
			"page_no", pageNo,
			"list_items", len(items),
			"new", newCount,
			"skip_exist", skipExist,
			"detail_fail", detailFail,
			"checkpoint", ck,
		)
		if ck != "" && ck == lastCheckpoint {
			pkglog.SourceInfo(name, "checkpoint 未推进，结束本轮翻页", "checkpoint", ck)
			break
		}
		lastCheckpoint = ck
		prog.Fingerprint = wantFP
		prog.Page = ck
		_ = r.store.SetProgress(name, prog)
	}

	if err := it.Err(); err != nil {
		pkglog.SourceWarn(name, "本轮迭代失败", "pages", pageNo, "err", err)
		if errors.Is(err, cookie.ErrCookieInvalid) {
			return wroteAny, fmt.Errorf("%w: %v", ErrUnrecoverable, err)
		}
		return wroteAny, err
	}

	pkglog.SourceInfo(name, "=== 本轮采集结束 ===", "pages", pageNo, "wrote_new", wroteAny, "checkpoint", lastCheckpoint)
	return wroteAny, nil
}

// fingerprintForSource 配置身份；变了就重置进度。源自己能算就用源的。
func (r *Runner) fingerprintForSource(src Source) string {
	if r.rt == nil || r.rt.Get() == nil {
		return src.Name()
	}
	app := r.rt.Get()
	if fp, ok := src.(interface {
		Fingerprint(*config.AppConfig) string
	}); ok {
		return fp.Fingerprint(app)
	}
	// 微博包装源没实现 Fingerprint 时，按超话/博主清单自己算
	if src.Name() == models.SourceWeibo.String() {
		var keys []string
		for _, id := range urls.WeiboContainerIDs(app.Collector.Weibo.SuperTopics) {
			keys = append(keys, "super:"+id)
		}
		for _, id := range urls.WeiboUIDs(app.Collector.Weibo.Users) {
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
		start, end, err := window.ResolveTimeRange(app.Collector.Douban.RangeFrom, app.Collector.Douban.RangeTo, now)
		if err == nil {
			return start, end
		}
	case models.SourceWeibo.String():
		start, end, err := window.ResolveTimeRange(app.Collector.Weibo.RangeFrom, "now", now)
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

func (r *Runner) Sources() []string {
	names := make([]string, 0, len(r.sources))
	for _, src := range r.sources {
		names = append(names, src.Name())
	}
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
