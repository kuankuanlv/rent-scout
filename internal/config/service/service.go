package service

import (
	"context"
	"time"

	"rent-scout/internal/config"
)

// Options 配置监视服务依赖
type Options struct {
	Hot      *config.HotConfig
	Interval time.Duration
}

// Service 管理 WatchDB 生命周期
type Service struct {
	hot      *config.HotConfig
	interval time.Duration
}

func New(opts Options) (*Service, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Service{hot: opts.Hot, interval: interval}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.hot == nil {
		<-ctx.Done()
		return nil
	}
	stop := s.hot.WatchDB(s.interval)
	defer stop()
	<-ctx.Done()
	return nil
}
