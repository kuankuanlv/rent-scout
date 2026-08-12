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
	ptr     atomic.Pointer[AppConfig]
	pubPath string
	envPath string
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
// 记录配置路径供 ReloadOnce 使用。
func (r *Runtime) Watch(pubPath, envPath string, interval time.Duration, notify func()) func() {
	r.pubPath = pubPath
	r.envPath = envPath
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
				_ = r.reloadOnce(notify)
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

// reloadOnce 重读公开配置与敏感配置；任一失败保留旧值（改坏配置不崩服务）
// 返回 error 便于 ReloadOnce 调用者判断成败
func (r *Runtime) reloadOnce(notify func()) error {
	if r.pubPath == "" {
		slog.Warn("配置热加载跳过：pubPath 未设置")
		return nil
	}
	next, err := Load(r.pubPath)
	if err != nil {
		slog.Warn("配置热加载失败，保留旧配置", "err", err)
		return err
	}
	if r.envPath != "" {
		if _, err := LoadEnvLocal(r.envPath); err != nil {
			slog.Warn("敏感配置热加载失败，保留旧配置", "err", err)
			return err
		}
	}
	r.ptr.Store(next) // 替换指针快照，不原地写
	if notify != nil {
		notify()
	}
	return nil
}

// ReloadOnce 立即重读公开配置与敏感配置（使用 Watch 记录的路径）
// 成功返回 nil，失败返回 error（旧配置保持不变）
func (r *Runtime) ReloadOnce() error {
	return r.reloadOnce(nil)
}
