package weibo

import (
	"context"
	"fmt"

	"rent-scout/internal/collector"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

// Source 微博占位：协程常驻，采集尚未实现
type Source struct{}

func New() *Source { return &Source{} }

func (s *Source) Name() string { return models.SourceWeibo.String() }

func (s *Source) List(context.Context, string) ([]collector.ListItem, string, error) {
	pkglog.Component(pkglog.SourceCollector(s.Name())).Info("微博采集尚未实现，无需执行")
	return nil, "", nil
}

func (s *Source) Detail(context.Context, collector.ListItem) (models.RentPost, error) {
	return models.RentPost{}, fmt.Errorf("微博采集尚未实现")
}
