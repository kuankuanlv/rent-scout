package pipeline

import (
	"context"
	"time"

	"rent-scout/internal/pkglog"
)

// FetchFunc 拉批函数：按 limit 从存储拉取一批待处理项
type FetchFunc[T any] func(ctx context.Context, limit int) ([]T, error)

// BatchFunc 批处理函数：处理一批项
type BatchFunc[T any] func(ctx context.Context, batch []T) error

// Options Consumer 参数（规格 2.3 协议语义）
type Options struct {
	BatchSize int           // 组批大小：积压达到即处理
	Linger    time.Duration // 兜底等待：积压不足时最长等多久处理一次
	Component string        // 日志 component：filter / notifier（调用方传入）
}

// Consumer 通用组批触发协议：trigger 主动触发（加速器，丢信号不致命）
// + linger 定时兜底（数据在库里，兜底必捞回，at-least-once）+ 批处理。
// filter/notifier 复用（规格 2.3）
type Consumer[T any] struct {
	fetch   FetchFunc[T]
	process BatchFunc[T]
	opts    Options
	trigger chan struct{} // 有界信号通道：满则丢（靠 linger 兜底）
	stop    chan struct{}
}

// New 创建 Consumer；未指定 BatchSize 时默认 20
func New[T any](fetch FetchFunc[T], process BatchFunc[T], opts Options) *Consumer[T] {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 20
	}
	return &Consumer[T]{
		fetch:   fetch,
		process: process,
		opts:    opts,
		trigger: make(chan struct{}, opts.BatchSize),
		stop:    make(chan struct{}),
	}
}

// Signal 上游写库成功后发非阻塞信号；通道满则丢信号（数据不丢，兜底轮询必捞回）
func (c *Consumer[T]) Signal() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

// Run 阻塞消费循环：trigger / linger 触发 → 拉批 → 处理 → 循环。
// ctx 取消或 Stop 调用后退出
func (c *Consumer[T]) Run(ctx context.Context) {
	// 防御：Linger<=0 时 time.NewTicker 会 panic；兜底用 1s（配置默认 120s 由调用方传）
	linger := c.opts.Linger
	if linger <= 0 {
		linger = time.Second
	}
	ticker := time.NewTicker(linger)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-c.trigger:
		case <-ticker.C:
		}
		// 拉批处理：空批跳过，错误记日志下轮再试（不误标记，状态机兜底）
		log := pkglog.Component(c.opts.Component)
		batch, err := c.fetch(ctx, c.opts.BatchSize)
		if err != nil {
			log.Error("[fetch_batch_failed] 拉批失败", "err", err)
			continue
		}
		if len(batch) == 0 {
			continue
		}
		if err := c.process(ctx, batch); err != nil {
			log.Error("[batch_process_failed] 批处理失败", "err", err, "count", len(batch))
		}
	}
}

// Stop 幂等停止（配合 ctx 双通道退出）
func (c *Consumer[T]) Stop() {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
}
