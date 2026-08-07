package config

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Runtime 并发安全的配置容器：atomic.Pointer 承载 AppConfig 快照。
// 模块 goroutine 通过 Get() 读快照（无锁）；热加载替换指针（不原地写）。
// 解决原 WatchReload `*cfg = *next` 在模块并发读下的 data race（最终审查遗留项）
type Runtime struct {
	ptr atomic.Pointer[AppConfig]
}

// NewRuntime 以当前配置创建运行时容器
func NewRuntime(cfg *AppConfig) *Runtime {
	r := &Runtime{}
	r.ptr.Store(cfg)
	return r
}

// Get 返回当前配置快照（调用方按需读取，不持有长期引用）
func (r *Runtime) Get() *AppConfig {
	return r.ptr.Load()
}

// Watch 每 interval 重读两份配置；解析失败保留旧值并 WARN。
// 变更后回调 notify（可空）；返回 stop 函数（幂等）。
func (r *Runtime) Watch(pubPath, envPath string, interval time.Duration, notify func()) func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r.reloadOnce(pubPath, envPath, notify)
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

// reloadOnce 重读公开配置与敏感配置；任一失败保留旧值（改坏配置不崩服务）
func (r *Runtime) reloadOnce(pubPath, envPath string, notify func()) {
	next, err := Load(pubPath)
	if err != nil {
		slog.Warn("配置热加载失败，保留旧配置", "err", err)
		return
	}
	if envPath != "" {
		if _, err := LoadEnvLocal(envPath); err != nil {
			slog.Warn("敏感配置热加载失败，保留旧配置", "err", err)
			return
		}
	}
	r.ptr.Store(next) // 替换指针快照，不原地写
	if notify != nil {
		notify()
	}
}
