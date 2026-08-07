package pkglog

import (
	"log/slog"
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
