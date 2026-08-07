package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// 默认配置文件路径与数据库路径（约定，db/ 目录 gitignore）
const (
	pubConfigPath = "config.toml"
	envConfigPath = "config.env.local.toml"
	dbPath        = "db/rent-scout.db"
)

func main() {
	// 加载配置：使用者入口文件必须存在（规格 7.2）
	cfg, err := config.Load(pubConfigPath)
	if err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}
	logger := pkglog.New(cfg.Log)
	rt := config.NewRuntime(cfg)
	logger.Info("rent-scout 启动", "addr", cfg.Server.Addr, "sources", cfg.Collector.Sources)

	// 打开数据库：建表幂等，重复启动安全
	db, err := store.Open(dbPath)
	if err != nil {
		logger.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("数据库就绪", "path", dbPath)

	// 敏感配置：启动即加载（collector cookie / notifier webhook 需要；缺失静默）
	envCfg, err := config.LoadEnvLocal(envConfigPath)
	if err != nil {
		logger.Error("加载敏感配置失败", "err", err)
		os.Exit(1)
	}

	// 配置热加载（Runtime 并发安全快照）
	stopReload := rt.Watch(pubConfigPath, envConfigPath, 10_000_000_000, nil)
	defer stopReload()

	// 下游（filter）触发通道：collector 写信号，计划 3 的 filter Consumer 消费
	trigger := make(chan struct{}, 64)

	// 启动采集模块：每源独立 goroutine（规格 4.5）。
	// 信号 ctx 直达 runner：SIGINT/SIGTERM 到达即取消（信号取消即停，最简接线）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := newCollectorRunner(rt, envCfg, db, trigger)
	if runner != nil {
		go runner.Run(ctx)
	}

	// 优雅退出：等待信号后收尾（模块 goroutine 已随 ctx 取消停止）
	<-ctx.Done()
	logger.Info("收到退出信号，正在关闭")
}

// newCollectorRunner 按配置构造采集调度器；无可用源时返回 nil（不启动采集）
func newCollectorRunner(rt *config.Runtime, env *config.EnvLocalConfig, db *store.Store, trigger chan<- struct{}) *collector.Runner {
	var sources []collector.Source
	for _, name := range rt.Get().Collector.Sources {
		switch name {
		case "douban":
			// cookie provider：按 envlocal cookie_mode 选择（规格 4.4）
			dc := env.Collector.Douban
			cookie, err := collector.NewCookieProvider(dc.CookieMode, dc.CookieFile, dc)
			if err != nil {
				slog.Error("douban cookie provider 初始化失败，使用匿名", "err", err)
				cookie, _ = collector.NewCookieProvider("none", "", dc)
			}
			d, err := collector.NewDouban(collector.DoubanOptions{
				GroupURLs: rt.Get().Collector.Douban.Groups,
				Cookie:    cookie,
			})
			if err != nil {
				slog.Error("douban 适配器初始化失败", "err", err)
				continue
			}
			sources = append(sources, d)
		default:
			slog.Warn("未知源，跳过", "source", name)
		}
	}
	if len(sources) == 0 {
		slog.Warn("没有可用的源，采集模块不启动")
		return nil
	}
	return collector.NewRunner(rt, env, db, sources, trigger)
}
