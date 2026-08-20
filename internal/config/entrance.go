package config

import (
	"context"
	"time"
)

// Options 配置监视服务依赖
type Options struct {
	Hot      *HotConfig
	Interval time.Duration
}

// ConfigService 管理 WatchDB 生命周期
type ConfigService struct {
	hot      *HotConfig    // 热配置容器（轮询 DB 后 COW 替换快照）
	interval time.Duration // DB 轮询间隔（默认 10 秒）
}

// --- 构造 ---

func New(opts Options) (*ConfigService, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &ConfigService{hot: opts.Hot, interval: interval}, nil
}

// --- 生命周期 ---

func (s *ConfigService) Run(ctx context.Context) error {
	if s == nil || s.hot == nil {
		<-ctx.Done()
		return nil
	}
	stop := s.hot.WatchDB(s.interval)
	defer stop()
	<-ctx.Done()
	return nil
}
