package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// trigger 主动触发：Signal 后立即拉批处理
func TestTriggerImmediate(t *testing.T) {
	var processed atomic.Int32
	done := make(chan struct{}, 1)
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		return []int{1, 2}, nil
	}, func(ctx context.Context, batch []int) error {
		processed.Add(int32(len(batch)))
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}, Options{BatchSize: 10, Linger: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	c.Signal() // 主动触发：不等 linger 立即处理
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Signal 后未立即处理")
	}
	if processed.Load() != 2 {
		t.Errorf("processed = %d, want 2", processed.Load())
	}
}

// linger 兜底：无信号时等待 Linger 后处理
func TestLingerFallback(t *testing.T) {
	var processed atomic.Int32
	done := make(chan struct{}, 1)
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		return []int{7}, nil
	}, func(ctx context.Context, batch []int) error {
		processed.Add(int32(len(batch)))
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}, Options{BatchSize: 10, Linger: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// 不发信号：靠 linger 兜底（50ms）
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("linger 兜底未触发")
	}
}

// 空批跳过：拉取无数据时不调用 process
func TestEmptyBatchSkipped(t *testing.T) {
	var processed atomic.Int32
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		return nil, nil // 空批
	}, func(ctx context.Context, batch []int) error {
		processed.Add(1)
		return nil
	}, Options{BatchSize: 10, Linger: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	time.Sleep(150 * time.Millisecond) // 多个 linger 周期
	if processed.Load() != 0 {
		t.Errorf("空批不应调用 process, got %d 次", processed.Load())
	}
}

// 批大小限制：一次拉取不超过 BatchSize
func TestBatchSizeLimit(t *testing.T) {
	var gotLimit int
	done := make(chan struct{}, 1)
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		gotLimit = limit
		select {
		case done <- struct{}{}:
		default:
		}
		return nil, nil
	}, func(ctx context.Context, batch []int) error {
		return nil
	}, Options{BatchSize: 5, Linger: 30 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("未拉取")
	}
	if gotLimit != 5 {
		t.Errorf("拉取 limit = %d, want 5", gotLimit)
	}
}

// WaitFull：不足批时 Signal 不处理，等 linger 才处理
func TestWaitFullSkipsPartialUntilLinger(t *testing.T) {
	var processed atomic.Int32
	done := make(chan struct{}, 1)
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		return []int{1}, nil
	}, func(ctx context.Context, batch []int) error {
		processed.Add(int32(len(batch)))
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}, Options{BatchSize: 3, Linger: 80 * time.Millisecond, WaitFull: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	c.Signal()
	time.Sleep(30 * time.Millisecond)
	if processed.Load() != 0 {
		t.Fatalf("未凑满不应处理, got %d", processed.Load())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("linger 未处理不足批")
	}
	if processed.Load() != 1 {
		t.Errorf("processed = %d, want 1", processed.Load())
	}
}

// WaitFull：库里已满批，Signal 立刻处理
func TestWaitFullProcessesWhenFull(t *testing.T) {
	var processed atomic.Int32
	done := make(chan struct{}, 1)
	c := New(func(ctx context.Context, limit int) ([]int, error) {
		return []int{1, 2, 3}, nil
	}, func(ctx context.Context, batch []int) error {
		processed.Add(int32(len(batch)))
		select {
		case done <- struct{}{}:
		default:
		}
		return nil
	}, Options{BatchSize: 3, Linger: time.Hour, WaitFull: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	c.Signal()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("满批 Signal 后未立即处理")
	}
	if processed.Load() != 3 {
		t.Errorf("processed = %d, want 3", processed.Load())
	}
}

// 上下文取消：Run 退出，goroutine 不泄漏
func TestRunStopsOnCancel(t *testing.T) {
	c := New(func(ctx context.Context, limit int) ([]int, error) { return nil, nil },
		func(ctx context.Context, batch []int) error { return nil },
		Options{BatchSize: 10, Linger: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	cancel() // 取消即退出
}
