package pkglog

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"

	"rent-scout/internal/config"
)

// 职责 component 取值（规格 §9）；业务日志用 Component 注入
const (
	Main      = "main"
	HotConfig = "hot_config"
	Collector = "collector"
	Filter    = "filter"
	Notifier  = "notifier"
	Admin     = "admin"
	Setup     = "setup"
)

// Component 返回带 component 属性的 logger（跟当前 Default）
func Component(name string) *slog.Logger {
	return slog.Default().With("component", name)
}

// New 按配置初始化全局 logger 并返回（规格 8.1）：
// format=json → JSONHandler；path 非空 → 文件轮转输出（50MB×5），空 → stdout
func New(cfg config.LogConfig) *slog.Logger {
	level := parseLevel(cfg.Level)
	var out io.Writer = os.Stdout
	if cfg.Path != "" {
		// 文件输出 + 大小轮转（规格 8.5：50MB × 5 份）
		out = &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    50, // MB
			MaxBackups: 5,
		}
	}
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(cfg.Format, "json") {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// parseLevel 解析配置字符串为 slog.Level；未知值降级 info
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
