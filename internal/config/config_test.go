package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 临时目录写公开配置，验证解析与默认值
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPublicConfig(t *testing.T) {
	path := writeConfig(t, `
[server]
addr = ":9090"

[collector]
sources = ["douban"]
interval = 600
jitter_ratio = 0.3

[filter]
ai_enabled = false

[log]
level = "debug"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// 显式配置生效
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Server.Addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Collector.Sources[0] != "douban" || cfg.Collector.Interval != 600 {
		t.Errorf("Collector 解析错误: %+v", cfg.Collector)
	}
	if cfg.Filter.AIEnabled == nil || *cfg.Filter.AIEnabled {
		t.Error("Filter.AIEnabled 应为 false")
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
}

func TestDefaultValues(t *testing.T) {
	path := writeConfig(t, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// 约定大于配置：缺省值必须可用（规格 7.2）
	if cfg.Server.Addr != ":8080" {
		t.Errorf("默认 Addr = %q, want :8080", cfg.Server.Addr)
	}
	if cfg.Collector.Interval != 1800 {
		t.Errorf("默认 Interval = %d, want 1800", cfg.Collector.Interval)
	}
	if cfg.Collector.JitterRatio != 0.2 {
		t.Errorf("默认 JitterRatio = %v, want 0.2", cfg.Collector.JitterRatio)
	}
	if cfg.Filter.AIEnabled == nil || !*cfg.Filter.AIEnabled {
		t.Error("默认 AIEnabled 应为 true")
	}
	if cfg.Pipeline.BatchSize != 20 {
		t.Errorf("默认 BatchSize = %d, want 20", cfg.Pipeline.BatchSize)
	}
	if cfg.Pipeline.LingerInterval != 120 {
		t.Errorf("默认 LingerInterval = %d, want 120", cfg.Pipeline.LingerInterval)
	}
	if cfg.Notifier.MaxAttempts != 3 {
		t.Errorf("默认 MaxAttempts = %d, want 3", cfg.Notifier.MaxAttempts)
	}
	if len(cfg.Collector.Douban.Groups) < 5 {
		t.Error("内置豆瓣小组应至少 5 个（开箱即用）")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("文件缺失应报错")
	}
}

// 显式 ai_enabled = false 必须保留，不被默认值覆盖
func TestAIEnabledExplicitFalse(t *testing.T) {
	path := writeConfig(t, `
[filter]
ai_enabled = false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Filter.AIEnabled == nil || *cfg.Filter.AIEnabled {
		t.Error("显式 ai_enabled = false 必须保留为 false")
	}
}

// 每源间隔覆盖语义：源配了 interval 用之，否则继承全局（规格 4.5）
func TestSourceIntervalOverride(t *testing.T) {
	path := writeConfig(t, `
[collector]
interval = 600

[collector.douban]
interval = 300
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Collector.SourceInterval("douban"); got != 300 {
		t.Errorf("douban 间隔 = %d, want 300（源覆盖）", got)
	}
	// 未覆盖的源（未来新增）继承全局
	if got := cfg.Collector.SourceInterval("beike"); got != 600 {
		t.Errorf("beike 间隔 = %d, want 600（全局继承）", got)
	}
	// 缺省：douban 无覆盖时用全局默认
	cfg2, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg2.Collector.SourceInterval("douban"); got != 1800 {
		t.Errorf("缺省 douban 间隔 = %d, want 1800", got)
	}
}
