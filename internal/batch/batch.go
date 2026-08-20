package batch

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
	BatchSize     int           // 组批大小：WaitFull 时凑满才处理；否则只是一次拉取上限
	Tick          time.Duration // 协程轮询；跟凑批等待无关；<=0 用 DefaultTick
	Linger        time.Duration // WaitFull 时不足批最多等多久才发；<=0 用 DefaultLinger
	Component     string        // 日志职责名：filter / ai_review / notifier
	WaitFull      bool          // true=凑满 BatchSize 或 linger 才处理；false=有信号立刻处理并可抽干
	LiveBatchSize func() int
	LiveLinger    func() time.Duration
}

// DefaultTick 各消费协程固定 1 分钟扫一次库，热读配置；业务间隔走 Linger
const DefaultTick = time.Minute

// DefaultLinger 公开配置已移除 linger 字段；代码侧固定兜底间隔
// AI 等模块的不足批兜底；通知走 notifier.interval
const DefaultLinger = 120 * time.Second

// Consumer 通用组批触发协议：trigger 主动触发（加速器，丢信号不致命）
// + tick 定时扫库（热读配置、捞漏）+ WaitFull 时 linger 才把不足批发掉。
// filter/notifier 复用（规格 2.3）
type Consumer[T any] struct {
	fetch        FetchFunc[T]
	process      BatchFunc[T]
	opts         Options
	trigger      chan struct{} // 有界信号通道：满则丢（靠 tick 兜底）
	stop         chan struct{}
	pendingSince time.Time // WaitFull 第一次看到不足批的时刻；发完清掉
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

func (c *Consumer[T]) batchSize() int {
	if c.opts.LiveBatchSize != nil {
		if n := c.opts.LiveBatchSize(); n > 0 {
			return n
		}
	}
	return c.opts.BatchSize
}

func (c *Consumer[T]) linger() time.Duration {
	if c.opts.LiveLinger != nil {
		if d := c.opts.LiveLinger(); d > 0 {
			return d
		}
	}
	if c.opts.Linger > 0 {
		return c.opts.Linger
	}
	return DefaultLinger
}

func (c *Consumer[T]) tick() time.Duration {
	if c.opts.Tick > 0 {
		return c.opts.Tick
	}
	return DefaultTick
}

// Run 阻塞消费循环：trigger / tick 触发 → 拉批 → 处理 → 循环。
// ctx 取消或 Stop 调用后退出
func (c *Consumer[T]) Run(ctx context.Context) {
	log := pkglog.Component(c.opts.Component)
	log.Info(startedLog(c.opts.Component))
	ticker := time.NewTicker(c.tick())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-c.trigger:
			log.Info("信号触发采集")
			c.step(ctx)
		case <-ticker.C:
			log.Info("定时轮询检查")
			c.step(ctx)
		}
	}
}

// step 拉一批处理。WaitFull 时不足批就等到 linger；满批立刻发。
// 不等批时满批再抽一轮，把积压抽干。
func (c *Consumer[T]) step(ctx context.Context) {
	log := pkglog.Component(c.opts.Component)
	batchSize := c.batchSize()
	linger := c.linger()
	for {
		batch, err := c.fetch(ctx, batchSize)
		if err != nil {
			log.Error("拉批失败", "err", err)
			return
		}
		if len(batch) == 0 {
			c.pendingSince = time.Time{}
			return
		}
		if c.opts.WaitFull && len(batch) < batchSize {
			if c.pendingSince.IsZero() {
				c.pendingSince = time.Now()
			}
			if time.Since(c.pendingSince) < linger {
				return
			}
		}
		if err := c.process(ctx, batch); err != nil {
			log.Error("批处理失败", "err", err, "count", len(batch))
			return
		}
		log.Info("批处理完成", "count", len(batch), "wait_full", c.opts.WaitFull)
		c.pendingSince = time.Time{}
		if c.opts.WaitFull || len(batch) < batchSize {
			return
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

func startedLog(component string) string {
	switch component {
	case pkglog.Filter:
		return "硬规则筛选协程已启动"
	case pkglog.AIReview:
		return "AI 审核协程已启动"
	case pkglog.Notifier:
		return "通知协程已启动"
	default:
		return "协程已启动"
	}
}
