package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// 默认配置文件路径；后续计划（admin HTTP）监听地址来自 cfg.Server.Addr
const (
	pubConfigPath   = "config.toml"
	envConfigPath   = "config.env.local.toml"
	dbPath          = "db/rent-scout.db" // 约定路径，gitignore 的 db/ 目录
)

func main() {
	// 加载配置：使用者入口文件必须存在（规格 7.2）
	cfg, err := config.Load(pubConfigPath)
	if err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}
	logger := pkglog.New(cfg.Log)
	logger.Info("rent-scout 启动", "addr", cfg.Server.Addr, "sources", cfg.Collector.Sources)

	// 配置并发安全容器：模块 goroutine 经 rt.Get() 读快照（计划 2 起 collector/filter/notifier 消费）
	rt := config.NewRuntime(cfg)

	// 打开数据库：建表幂等，重复启动安全
	db, err := store.Open(dbPath)
	if err != nil {
		logger.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("数据库就绪", "path", dbPath)

	// 配置热加载：10s 轮询替换快照指针，改配置即时生效（规格 7.2）
	stopReload := rt.Watch(pubConfigPath, envConfigPath, 10_000_000_000, nil)
	defer stopReload()

	// 优雅退出：SIGINT/SIGTERM 后排空收尾（后续计划接入 collector/filter/notifier）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logger.Info("收到退出信号，正在关闭")
}
