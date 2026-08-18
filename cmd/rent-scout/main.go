package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	adminservice "rent-scout/internal/admin/service"
	"rent-scout/internal/app"
	collectorservice "rent-scout/internal/collector/service"
	configservice "rent-scout/internal/config/service"
	filterservice "rent-scout/internal/filter/service"
	notifierservice "rent-scout/internal/notifier/service"
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

	filterSvc, err := filterservice.New(filterservice.Options{
		Config: resources.Config,
		Store:  resources.Store,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("筛选模块初始化失败", "err", err)
		os.Exit(1)
	}

	collectorSvc, err := collectorservice.New(collectorservice.Options{
		Config:        resources.Config,
		Store:         resources.Store,
		OnPostCreated: filterSvc.SignalCollected,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("采集模块初始化失败", "err", err)
		os.Exit(1)
	}

	notifierSvc, err := notifierservice.New(notifierservice.Options{
		Config: resources.Config,
		Store:  resources.Store,
	})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("通知模块初始化失败", "err", err)
		os.Exit(1)
	}
	// 筛选落库完成 → 立即拉批通知（不再等 linger 兜底）
	filterSvc.SetOnNotifyReady(notifierSvc.Signal)

	cfgSvc, err := configservice.New(configservice.Options{Hot: resources.Config})
	if err != nil {
		pkglog.Component(pkglog.Main).Error("配置模块初始化失败", "err", err)
		os.Exit(1)
	}

	adminSvc, err := adminservice.New(adminservice.Options{
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
