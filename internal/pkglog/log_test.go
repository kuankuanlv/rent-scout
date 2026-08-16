package pkglog

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
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

func TestDefaultLogDir(t *testing.T) {
	t.Setenv("LOG_DIR", "")
	if got := defaultLogDir(); got != defaultLogsRel {
		t.Fatalf("got %q", got)
	}
	t.Setenv("LOG_DIR", "/tmp/mylogs")
	if got := defaultLogDir(); got != "/tmp/mylogs" {
		t.Fatalf("env got %q", got)
	}
}

func TestDailyFileWrites(t *testing.T) {
	dir := t.TempDir()
	w := newDailyFile(dir)
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, logFilePrefix+"*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("files=%v", matches)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello\n" {
		t.Fatalf("content=%q", b)
	}
}

func TestClipHubAttrs(t *testing.T) {
	if got := clipHubAttrs("short"); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("啊", maxHubAttrs+10)
	got := clipHubAttrs(long)
	if !strings.Contains(got, "完整内容见 logs") {
		t.Fatalf("应截断: %s", got[len(got)-40:])
	}
}
