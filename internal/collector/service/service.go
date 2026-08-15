package service

import (
	"context"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/collector/sources/douban"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
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
	log := pkglog.Component(pkglog.Main)
	rt, db := opts.Config, opts.Store
	cfg := rt.Get()
	var sources []collector.Source
	for _, name := range cfg.Collector.Sources {
		src, ok := models.ParseSource(name)
		if !ok {
			log.Warn("未知采集源", "source", name)
			continue
		}
		switch src {
		case models.SourceDouban:
			cp := cookie.NewHotConfigProvider(rt)
			d, err := douban.NewDouban(douban.DoubanOptions{
				GroupURLs: cfg.Collector.Douban.Groups,
				Cookie:    cp,
			})
			if err != nil {
				log.Error("源初始化失败", "source", name, "err", err)
				continue
			}
			sources = append(sources, d)
		default:
			log.Warn("未知采集源", "source", name)
		}
	}
	trigger := make(chan struct{}, postCreatedCap)
	var runner *collector.Runner
	if len(sources) == 0 {
		log.Warn("采集未启动")
	} else {
		runner = collector.NewRunner(rt, db, sources, trigger)
	}
	return &Service{
		rt:            rt,
		db:            db,
		runner:        runner,
		syncer:        cookie.NewSyncer(rt, db, cookie.DefaultSyncInterval),
		trigger:       trigger,
		onPostCreated: opts.OnPostCreated,
	}, nil
}

// Controller 无可用源时返回 nil，管理台按采集未启动处理
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
