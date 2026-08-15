package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type waitSvc struct {
	started *atomic.Int32
}

func (s *waitSvc) Run(ctx context.Context) error {
	s.started.Add(1)
	<-ctx.Done()
	return nil
}

func TestRunStartsAllAndShutdownBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	a := &waitSvc{started: &started}
	b := &waitSvc{started: &started}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, a, b) }()

	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 2 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("未全部启动: %d", started.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("关闭超时超过预算")
	}
}

func TestRunSkipsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	done := make(chan error, 1)
	go func() { done <- Run(ctx, nil, &waitSvc{started: &started}) }()
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 1 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("未启动")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
