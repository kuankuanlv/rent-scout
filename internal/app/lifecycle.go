package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"rent-scout/internal/pkglog"
)

// Service 长期运行模块入口
type Service interface {
	Run(ctx context.Context) error
}

const shutdownBudget = 5 * time.Second

// Run 按传入顺序在独立协程启动各服务，共享 ctx。
// 任一服务返回错误只记日志并保留首个错误，不中断其它服务；
// 主协程阻塞到 ctx 取消，再等所有服务协程退出（最多 shutdownBudget）。
func Run(ctx context.Context, services ...Service) error {
	log := pkglog.Component(pkglog.Main)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error

	// 每个服务一条常驻协程；错误只记日志，互不影响
	for _, svc := range services {
		if svc == nil {
			continue
		}
		wg.Add(1)
		go runService(ctx, &wg, &mu, &first, svc, log)
	}

	// 主协程阻塞到 ctx 取消（信号/超时），然后统一收尾
	<-ctx.Done()
	log.Info("正在关闭")
	waitShutdown(&wg, log)

	mu.Lock()
	defer mu.Unlock()
	return first
}

// runService 单服务协程：运行到 ctx 取消或自身退出；首个错误保留给 Run 返回。
func runService(ctx context.Context, wg *sync.WaitGroup, mu *sync.Mutex, first *error, svc Service, log *slog.Logger) {
	defer wg.Done()
	if err := svc.Run(ctx); err != nil {
		log.Error("模块运行失败", "err", err)
		mu.Lock()
		if *first == nil {
			*first = err
		}
		mu.Unlock()
	}
}

// waitShutdown 等所有服务协程退出；超过 shutdownBudget 记超时警告，不无限等。
func waitShutdown(wg *sync.WaitGroup, log *slog.Logger) {
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
}
