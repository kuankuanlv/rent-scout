package collector

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Runner 采集调度：每源独立 goroutine（规格 4.5，源间并发）。
// 单轮流程（调整规格 E）：List → 时间窗过滤（超窗停页）→ 批量查重
// → 仅新帖 Detail → 入库 → 游标 → 触发信号；轮间间隔 ±jitter 抖动，
// 失败指数退避（防检测，规格 4.5）
type Runner struct {
	rt      *config.Runtime
	env     *config.EnvLocalConfig
	store   *store.Store
	sources []Source
	trigger chan<- struct{}
}

// NewRunner 创建调度器；trigger 为下游（filter）信号通道
func NewRunner(rt *config.Runtime, env *config.EnvLocalConfig, st *store.Store, sources []Source, trigger chan<- struct{}) *Runner {
	return &Runner{rt: rt, env: env, store: st, sources: sources, trigger: trigger}
}

// Run 启动全部源的独立 goroutine（源间并发，互不阻塞）；ctx 取消即全部停止
func (r *Runner) Run(ctx context.Context) {
	for _, src := range r.sources {
		go r.runSource(ctx, src)
	}
}

// runSource 单源循环：调度 → 退避 → 抖动休眠
func (r *Runner) runSource(ctx context.Context, src Source) {
	interval := time.Duration(r.rt.Get().Collector.SourceInterval(src.Name())) * time.Second
	jitter := r.rt.Get().Collector.JitterRatio
	failStreak := 0
	for {
		// 单轮采集；失败指数退避后重试（封禁/限流时自动拉开频率）
		err := r.runSourceOnce(ctx, src, r.trigger)
		if err != nil {
			failStreak++
			backoff := time.Duration(1<<min(failStreak-1, 5)) * time.Minute
			slog.Warn("采集失败，退避重试", "source", src.Name(), "err", err, "backoff_s", int(backoff.Seconds()), "attempt", failStreak)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		failStreak = 0
		// 间隔 ±jitter 随机抖动（防固定规律被检测，规格 4.5）
		wait := jittered(interval, jitter)
		slog.Info("本轮采集完成，等待下一轮", "source", src.Name(), "wait_s", int(wait.Seconds()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
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
