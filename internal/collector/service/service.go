package service

import (
	"context"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/collector/sources/douban"
	"rent-scout/internal/collector/sources/weibo"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

const postCreatedCap = 64

// Options 采集服务依赖
type Options struct {
	Config        *config.HotConfig
	Store         *store.Store
	OnPostCreated func()
}

// Service 采集调度 + Cookie 同步
type Service struct {
	rt            *config.HotConfig
	db            *store.Store
	runner        *collector.Runner
	syncer        *cookie.Syncer
	trigger       chan struct{}
	onPostCreated func()
}

func New(opts Options) (*Service, error) {
	rt, db := opts.Config, opts.Store
	cp := cookie.NewHotConfigProvider(rt)
	d, err := douban.NewDouban(douban.DoubanOptions{Config: rt, Cookie: cp})
	if err != nil {
		return nil, err
	}
	w := weibo.New(weibo.Options{Config: rt, Cookie: cp})
	trigger := make(chan struct{}, postCreatedCap)
	runner := collector.NewRunner(rt, db, []collector.Source{d, w}, trigger)
	return &Service{
		rt:            rt,
		db:            db,
		runner:        runner,
		syncer:        cookie.NewSyncer(rt, db, cookie.DefaultSyncInterval),
		trigger:       trigger,
		onPostCreated: opts.OnPostCreated,
	}, nil
}

// Controller 管理台源控制；协程常驻，即使配置里源全关也返回自身
func (s *Service) Controller() *Service {
	if s == nil || s.runner == nil {
		return nil
	}
	return s
}

func (s *Service) Sources() []string {
	if s == nil || s.runner == nil {
		return nil
	}
	return s.runner.Sources()
}

func (s *Service) SetEnabled(name string, on bool) error {
	return s.runner.SetEnabled(name, on)
}

func (s *Service) Trigger(name string) error {
	return s.runner.Trigger(name)
}

func (s *Service) SourceEnabled(name string) bool {
	return s.runner.SourceEnabled(name)
}

func (s *Service) Run(ctx context.Context) error {
	if s.runner != nil {
		go s.runner.Run(ctx)
	}
	go s.syncer.Run(ctx)
	go s.bridge(ctx)
	<-ctx.Done()
	return nil
}

func (s *Service) bridge(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			if s.onPostCreated != nil {
				s.onPostCreated()
			}
		}
	}
}
