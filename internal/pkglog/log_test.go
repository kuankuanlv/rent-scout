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

func TestComponentAttr(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
	Component(HotConfig).Info("[hot_config_load] 配置变更，开始 COW 更换快照", "keys", 1)
	out := buf.String()
	if !strings.Contains(out, "component=hot_config") || !strings.Contains(out, "[hot_config_load]") {
		t.Fatalf("Component 日志缺字段: %s", out)
	}
}
