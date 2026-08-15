package weibo

import (
	"context"
	"fmt"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

// Source 微博占位：协程常驻，采集尚未实现
type Source struct {
	rt *config.HotConfig
}

func New(rt *config.HotConfig) *Source { return &Source{rt: rt} }

func (s *Source) Name() string { return models.SourceWeibo.String() }

func (s *Source) urls() []string {
	if s == nil || s.rt == nil {
		return nil
	}
	if app := s.rt.Get(); app != nil {
		return config.HTTPURLs(app.Collector.Weibo.URLs)
	}
	return nil
}

func (s *Source) List(context.Context, string) ([]collector.ListItem, string, error) {
	log := pkglog.Component(pkglog.SourceCollector(s.Name()))
	if len(s.urls()) == 0 {
		log.Info("当前配置微博超话 URL 为空，无需执行")
		return nil, "", nil
	}
	log.Info("微博采集尚未实现，无需执行", "urls", len(s.urls()))
	return nil, "", nil
}

func (s *Source) Detail(context.Context, collector.ListItem) (models.RentPost, error) {
	return models.RentPost{}, fmt.Errorf("微博采集尚未实现")
}
