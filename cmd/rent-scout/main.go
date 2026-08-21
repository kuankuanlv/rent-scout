package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"rent-scout/internal/admin/core"
	"rent-scout/internal/app"
	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/collector/sources/douban"
	"rent-scout/internal/collector/sources/weibo"
	"rent-scout/internal/config"
	"rent-scout/internal/filter"
	"rent-scout/internal/notifier"
	"rent-scout/internal/pkglog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	resources, cleanup, err := app.Bootstrap(app.Options{})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("启动失败", "err", err)
		os.Exit(1)
	}
	defer cleanup()

	filterSvc, err := filter.New(filter.Options{
		Config: resources.Config,
		Store:  resources.Store,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("筛选模块初始化失败", "err", err)
		os.Exit(1)
	}

	collectorSvc, err := collector.New(collector.Options{
		Config:        resources.Config,
		Store:         resources.Store,
		Sources:       collectorSources(resources.Config),
		OnPostCreated: filterSvc.SignalCollected,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("采集模块初始化失败", "err", err)
		os.Exit(1)
	}

	notifierSvc, err := notifier.New(notifier.Options{
		Config: resources.Config,
		Store:  resources.Store,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("通知模块初始化失败", "err", err)
		os.Exit(1)
	}
	// 筛选落库完成 → 立即拉批通知（不再等 linger 兜底）
	filterSvc.SetOnNotifyReady(notifierSvc.Signal)

	cfgSvc, err := config.New(config.Options{Hot: resources.Config})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("配置模块初始化失败", "err", err)
		os.Exit(1)
	}

	adminSvc, err := core.New(core.Options{
		Config:         resources.Config,
		Store:          resources.Store,
		Sources:        collectorSvc.Controller(),
		OnRulesChanged: filterSvc.SignalRulesChanged,
		NotifyManual:   notifierSvc,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("管理面初始化失败", "err", err)
		os.Exit(1)
	}

	if err := app.Run(ctx, cfgSvc, collectorSvc, filterSvc, notifierSvc, adminSvc); err != nil {
		os.Exit(1)
	}
}

func collectorSources(rt *config.HotConfig) []collector.Source {
	cp := cookie.NewHotConfigProvider(rt)
	d, err := douban.NewDouban(douban.DoubanOptions{Config: rt, Cookie: cp})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("豆瓣源初始化失败", "err", err)
		os.Exit(1)
	}
	w := weibo.New(weibo.Options{Config: rt, Cookie: cp})
	return []collector.Source{d, w}
}
