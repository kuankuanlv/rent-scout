package pkglog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLevels(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"":      slog.LevelInfo, // 缺省 info
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// 未知级别降级为 info，不报错
func TestNewUnknownLevel(t *testing.T) {
	if got := parseLevel("verbose"); got != slog.LevelInfo {
		t.Errorf("未知级别应降级 info, got %v", got)
	}
}

func TestDutyPrefixText(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&dutyHandler{inner: inner, json: false}))
	Component(HotConfig).Info("配置变更，开始 COW 更换快照", "keys", 1)
	out := buf.String()
	if !strings.Contains(out, "[hot_config_reload]") {
		t.Fatalf("缺职责前缀: %s", out)
	}
	if strings.Contains(out, "duty=") {
		t.Fatalf("文本日志不应再打 duty 字段: %s", out)
	}
}

func TestDutyJSONField(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&dutyHandler{inner: inner, json: true}))
	Component(Notifier).Info("渠道分组发送", "channel", "feishu")
	out := buf.String()
	if !strings.Contains(out, `"duty":"notifier"`) {
		t.Fatalf("JSON 应有 duty 字段: %s", out)
	}
	if strings.Contains(out, `[notifier]`) {
		t.Fatalf("JSON 消息不应再包 []: %s", out)
	}
}

func TestSourceCollector(t *testing.T) {
	if got := SourceCollector("douban"); got != "douban_collector" {
		t.Errorf("got %q", got)
	}
	if got := SourceCollector(""); got != Collector {
		t.Errorf("空源兜底 = %q", got)
	}
}
