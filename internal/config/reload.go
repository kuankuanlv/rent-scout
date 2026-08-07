package config

import (
	"log/slog"
	"sync"
	"time"
)

// WatchReload 每 interval 重读两份配置，原地更新 cfg（指针内容可变，调用方无需感知）。
// 变更后回调 notify（可空）。返回 stop 函数用于优雅退出。
// 解析失败保留旧值并记 WARN（改坏配置不至于崩掉服务）。
func WatchReload(cfg *AppConfig, pubPath, envPath string, interval time.Duration, notify func()) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				reloadOnce(cfg, pubPath, envPath, notify)
			}
		}
	}()
	// sync.Once 保证 stop 幂等：多次调用不会重复 close 导致 panic
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
	}
}

// reloadOnce 重读公开配置并原地更新；敏感配置单独校验可解析（值对象，模块按需读取，不缓存）。
// 任一解析失败保留旧值并记 WARN（改坏配置不至于崩掉服务）
func reloadOnce(cfg *AppConfig, pubPath, envPath string, notify func()) {
	next, err := Load(pubPath)
	if err != nil {
		slog.Warn("配置热加载失败，保留旧配置", "err", err)
		return
	}
	// 敏感配置改动也触发回调（渠道 webhook 等变更即时生效）
	if envPath != "" {
		if _, err := LoadEnvLocal(envPath); err != nil {
			slog.Warn("敏感配置热加载失败，保留旧配置", "err", err)
			return
		}
	}
	*cfg = *next // 原地替换整个结构
	if notify != nil {
		notify()
	}
}
