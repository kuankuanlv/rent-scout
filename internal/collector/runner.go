package collector

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Runner 采集调度：每源独立 goroutine（规格 4.5，源间并发）。
// 单轮流程（调整规格 E）：List → 时间窗过滤（超窗停页）→ 批量查重
// → 仅新帖 Detail → 入库 → 游标 → 触发信号；轮间间隔 ±jitter 抖动，
// 失败指数退避（防检测，规格 4.5）。
// 控制面（规格 7.1）：enabled 启停 + manual 手动触发（容量 1 非阻塞），
// 由 /api/sources 经 SourceController 接口驱动
type Runner struct {
	rt      *config.Runtime
	env     *config.EnvLocalConfig
	store   *store.Store
	sources []Source
	trigger chan<- struct{}
	mu      sync.Mutex
	enabled map[string]bool          // 源启用状态，默认 true
	manual  map[string]chan struct{} // 手动触发信号（容量 1，非阻塞）
}

// NewRunner 创建调度器；trigger 为下游（filter）信号通道。
// 初始化 enabled/manual 映射：全部源默认启用 + 各建容量 1 手动触发通道
func NewRunner(rt *config.Runtime, env *config.EnvLocalConfig, st *store.Store, sources []Source, trigger chan<- struct{}) *Runner {
	enabled := make(map[string]bool, len(sources))
	manual := make(map[string]chan struct{}, len(sources))
	for _, src := range sources {
		enabled[src.Name()] = true
		manual[src.Name()] = make(chan struct{}, 1)
	}
	return &Runner{rt: rt, env: env, store: st, sources: sources, trigger: trigger,
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
	slog.Info("源启停", "source", name, "enabled", on)
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
	interval := time.Duration(r.rt.Get().Collector.SourceInterval(src.Name())) * time.Second
	jitter := r.rt.Get().Collector.JitterRatio
	failStreak := 0
	for {
		// 停用态：等待手动触发或周期轮询恢复
		if !r.SourceEnabled(src.Name()) {
			slog.Info("源已停用，等待恢复", "source", src.Name())
			select {
			case <-ctx.Done():
				return
			case <-r.manual[src.Name()]:
				// 手动触发：即使停用也跑一轮（规格 7.1 手动触发抓取）
				if err := r.runSourceOnce(ctx, src, r.trigger); err != nil {
					slog.Warn("手动触发采集失败", "source", src.Name(), "err", err)
				} else {
					failStreak = 0
				}
			case <-time.After(time.Second):
				// 周期轮询：仅用于恢复判定，不执行轮次
			}
			continue
		}
		// 等待时长：失败指数退避优先，成功间隔 ±jitter 抖动（保持原退避语义）
		wait := jittered(interval, jitter)
		if failStreak > 0 {
			wait = time.Duration(1<<min(failStreak-1, 5)) * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-r.manual[src.Name()]:
			// 手动触发：立即跑一轮，随后正常计时；成功重置退避
			failStreak = 0
			if err := r.runSourceOnce(ctx, src, r.trigger); err != nil {
				slog.Warn("手动触发采集失败", "source", src.Name(), "err", err)
			}
		case <-time.After(wait):
			// 定时轮次：失败指数退避后重试（封禁/限流时自动拉开频率）
			err := r.runSourceOnce(ctx, src, r.trigger)
			if err != nil {
				failStreak++
				backoff := time.Duration(1<<min(failStreak-1, 5)) * time.Minute
				slog.Warn("采集失败，退避重试", "source", src.Name(), "err", err, "backoff_s", int(backoff.Seconds()), "attempt", failStreak)
				continue // 下一轮循环：failStreak>0 → wait=backoff
			}
			failStreak = 0
			slog.Info("本轮采集完成，等待下一轮", "source", src.Name(), "wait_s", int(wait.Seconds()))
		}
	}
}

// runSourceOnce 单轮采集（可测：测试直接调用它）
func (r *Runner) runSourceOnce(ctx context.Context, src Source, trigger chan<- struct{}) error {
	cfg := r.rt.Get()
	maxAge := cfg.Collector.MaxAgeDays
	cutoff := time.Now().Add(-time.Duration(maxAge) * 24 * time.Hour)

	// 游标断点续传（规格 3.5）：上次位置继续
	cursorVal, _, _ := r.store.GetCursor(src.Name())
	slog.Info("采集开始", "source", src.Name(), "cursor", cursorVal, "max_age_days", maxAge)

	newPosts := 0
	for {
		items, next, err := src.List(ctx, cursorVal)
		if err != nil {
			return fmt.Errorf("列表页: %w", err)
		}
		// 时间窗过滤：列表按时间倒序，超窗即停止翻页（调整规格 D）
		var fresh []ListItem
		stop := false
		for _, it := range items {
			if it.PublishedAt.Before(cutoff) {
				stop = true
				break
			}
			fresh = append(fresh, it)
		}
		if len(fresh) == 0 {
			// 空页/超窗页：本页无新帖。跟随源协议推进游标（douban 空组 next 指向下一组），
			// 避免多组配置下卡死在空组（P2-9 审查发现）；next=="" 表示已到末尾
			if next != "" {
				cursorVal = next
				if err := r.store.SetCursor(src.Name(), cursorVal); err != nil {
					return err
				}
			}
			break
		}
		// 列表页先行批量查重：已存在的零详情页请求（调整规格 E）
		ids := make([]string, 0, len(fresh))
		for _, it := range fresh {
			ids = append(ids, it.ExternalID)
		}
		existing, err := r.store.ExistsByExternalIDs(src.Name(), ids)
		if err != nil {
			return err
		}
		for _, it := range fresh {
			if existing[it.ExternalID] {
				continue // 已抓过：跳过详情页
			}
			post, err := src.Detail(ctx, it)
			if err != nil {
				slog.Warn("详情页失败，跳过该条", "source", src.Name(), "id", it.ExternalID, "err", err)
				continue
			}
			// P2-7 审查：douban Detail 未设 CollectedAt，而 posts 表 collected_at NOT NULL
			if post.CollectedAt.IsZero() {
				post.CollectedAt = time.Now()
			}
			added, err := r.store.InsertPost(post)
			if err != nil {
				return fmt.Errorf("入库: %w", err)
			}
			if added {
				newPosts++
			}
		}
		cursorVal = next
		if err := r.store.SetCursor(src.Name(), cursorVal); err != nil {
			return err
		}
		if next == "" || stop {
			break
		}
	}
	// 新帖触发下游（filter）信号；空批不发（规格 2.3 协议）
	if newPosts > 0 && trigger != nil {
		select {
		case trigger <- struct{}{}:
		default: // 满则丢：数据在库，下游 linger 兜底
		}
	}
	slog.Info("采集完成", "source", src.Name(), "new_posts", newPosts)
	return nil
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
