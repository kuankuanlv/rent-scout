package app

import (
	"context"
	"sync"
	"time"

	"rent-scout/internal/pkglog"
)

// Service 长期运行模块入口
type Service interface {
	Run(ctx context.Context) error
}

const shutdownBudget = 5 * time.Second

// Run 按传入顺序在独立协程启动各服务，共享 ctx；运行错误只记日志，等 ctx 取消后再等最多 5 秒。
func Run(ctx context.Context, services ...Service) error {
	log := pkglog.Component(pkglog.Main)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for _, svc := range services {
		if svc == nil {
			continue
		}
		wg.Add(1)
		go func(s Service) {
			defer wg.Done()
			if err := s.Run(ctx); err != nil {
				log.Error("模块运行失败", "err", err)
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}(svc)
	}

	<-ctx.Done()
	log.Info("正在关闭")
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownBudget):
		log.Warn("关闭超时")
	}
	mu.Lock()
	defer mu.Unlock()
	return first
}
