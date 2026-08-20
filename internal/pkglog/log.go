package pkglog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Options 日志初始化参数；由 bootstrap 从配置映射过来，本包不依赖 config
type Options struct {
	Level       string
	Format      string
	Path        string
	MemoryLines int
}

// 协程职责名：方括号只包这个，不进消息正文
const (
	Main              = "main"
	HotConfig         = "hot_config_reload" // 热更新配置协程
	Collector         = "collector"         // 未分源时的兜底
	Filter            = "filter"            // 硬规则筛选，不等批
	AIReview          = "ai_review"         // AI 审核凑批协程
	Notifier          = "notifier"
	Admin             = "admin"
	Setup             = "setup"
	DoubanCookieCloud = "douban_cookie_cloud" // CookieCloud 同步协程，与 douban_collector 隔离
	WeiboCookieCloud  = "weibo_cookie_cloud"
)

const dutyKey = "duty"

// SourceCollector 采集协程职责：douban → douban_collector
func SourceCollector(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return Collector
	}
	return source + "_collector"
}

// SourceCookieCloud CookieCloud 同步职责：weibo → weibo_cookie_cloud
func SourceCookieCloud(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return DoubanCookieCloud
	}
	return source + "_cookie_cloud"
}

// SourceInfo / SourceWarn / SourceError 打到 [xxx_collector]（原 internal/log 薄封装，已并入本包）
func SourceInfo(source, msg string, args ...any) {
	Component(SourceCollector(source)).Info(msg, args...)
}

func SourceWarn(source, msg string, args ...any) {
	Component(SourceCollector(source)).Warn(msg, args...)
}

func SourceError(source, msg string, args ...any) {
	Component(SourceCollector(source)).Error(msg, args...)
}

// Component 绑定职责名；文本日志由 handler 写成 [duty] 前缀
func Component(name string) *slog.Logger {
	return slog.Default().With(dutyKey, name)
}

// New 按配置初始化全局 logger 并返回（规格 8.1）：
// format=json → JSONHandler；path 非空 → 文件轮转输出（50MB×5），空 → stdout
func New(cfg Options) *slog.Logger {
	level := parseLevel(cfg.Level)
	writers := []io.Writer{os.Stdout}
	if dir, err := ensureLogDir(defaultLogDir()); err == nil {
		writers = append(writers, newDailyFile(dir))
		if abs, err := filepath.Abs(dir); err == nil {
			fmt.Fprintf(os.Stderr, "日志写入 %s\n", abs)
		}
	} else {
		fmt.Fprintf(os.Stderr, "日志目录不可用，只打控制台: %v\n", err)
	}
	if cfg.Path != "" {
		writers = append(writers, &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    50,
			MaxBackups: 5,
		})
	}
	out := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{Level: level}
	var inner slog.Handler
	json := strings.EqualFold(cfg.Format, "json")
	if json {
		inner = slog.NewJSONHandler(out, opts)
	} else {
		inner = slog.NewTextHandler(out, opts)
	}
	logger := slog.New(&dutyHandler{inner: inner, json: json})
	slog.SetDefault(logger)
	SetHubCap(cfg.MemoryLines)
	return logger
}

// dutyHandler 文本把 [duty] 打在消息前；JSON 用 duty 字段。消息本身不再写 []
type dutyHandler struct {
	inner slog.Handler
	duty  string
	json  bool
}

func (h *dutyHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *dutyHandler) Handle(ctx context.Context, r slog.Record) error {
	duty := h.duty
	if duty == "" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == dutyKey {
				duty = a.Value.String()
			}
			return true
		})
	}
	pushHub(duty, r)
	if duty == "" {
		return h.inner.Handle(ctx, r)
	}
	if h.json {
		return h.inner.Handle(ctx, r)
	}
	if !strings.HasPrefix(r.Message, "[") {
		r.Message = "[" + duty + "] " + r.Message
	}
	return h.inner.Handle(ctx, r)
}

func (h *dutyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	duty := h.duty
	rest := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if a.Key == dutyKey {
			duty = a.Value.String()
			if h.json {
				rest = append(rest, a)
			}
			continue
		}
		rest = append(rest, a)
	}
	return &dutyHandler{inner: h.inner.WithAttrs(rest), duty: duty, json: h.json}
}

func (h *dutyHandler) WithGroup(name string) slog.Handler {
	return &dutyHandler{inner: h.inner.WithGroup(name), duty: h.duty, json: h.json}
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
