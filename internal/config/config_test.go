package config

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/store"
)

func TestDefaultValues(t *testing.T) {
	cfg := DefaultApp()
	if cfg.Server.Addr != ":7777" {
		t.Errorf("默认 Addr = %q, want :7777", cfg.Server.Addr)
	}
	if cfg.Collector.Interval != 1800 {
		t.Errorf("默认 Interval = %d, want 1800", cfg.Collector.Interval)
	}
	if cfg.Filter.AIEnabled == nil || !*cfg.Filter.AIEnabled {
		t.Error("默认 AIEnabled 应为 true")
	}
	if len(cfg.Collector.Douban.Groups) < 5 {
		t.Error("内置豆瓣小组应至少 5 个")
	}
}

func TestKVRoundTrip(t *testing.T) {
	app := DefaultApp()
	app.Server.Addr = ":9090"
	app.Log.Level = "debug"
	disabled := false
	app.Filter.AIEnabled = &disabled
	env := DefaultEnv()
	env.Filter.LLM.APIKey = "sk-test"
	env.Notifier.Feishu.Webhook = "https://example.com/hook"

	kv := MergeKV(AppToKV(app), EnvToKV(env))
	gotApp := KVToApp(kv)
	gotEnv := KVToEnv(kv)

	if gotApp.Server.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090", gotApp.Server.Addr)
	}
	if gotApp.Log.Level != "debug" {
		t.Errorf("Log.Level = %q", gotApp.Log.Level)
	}
	if gotApp.Filter.AIEnabled == nil || *gotApp.Filter.AIEnabled {
		t.Error("AIEnabled 应为 false")
	}
	if gotEnv.Filter.LLM.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", gotEnv.Filter.LLM.APIKey)
	}
}

func TestRuntimeFromDB(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "cfg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	kv := AppToKV(DefaultApp())
	kv["server.addr"] = ":8888"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	if rt.Get().Server.Addr != ":8888" {
		t.Errorf("Addr = %q, want :8888", rt.Get().Server.Addr)
	}
}

func TestSourceIntervalOverride(t *testing.T) {
	cfg := KVToApp(map[string]string{
		"collector.interval":        "600",
		"collector.douban.interval": "300",
		"collector.sources":         "douban",
	})
	if got := cfg.Collector.SourceInterval("douban"); got != 300 {
		t.Errorf("douban = %d, want 300", got)
	}
}

func TestValidateApp(t *testing.T) {
	if errs := ValidateApp(DefaultApp()); len(errs) != 0 {
		t.Errorf("默认配置应通过校验: %v", errs)
	}
	bad := &AppConfig{}
	if errs := ValidateApp(bad); len(errs) == 0 {
		t.Error("空配置应校验失败")
	}
}

func TestValidateEnvCookieMode(t *testing.T) {
	if errs := ValidateEnv(DefaultEnv()); len(errs) != 0 {
		t.Errorf("DefaultEnv 应通过: %v", errs)
	}
	if errs := ValidateEnv(&EnvLocalConfig{}); len(errs) != 0 {
		t.Errorf("空 CookieMode 应视为 none 通过: %v", errs)
	}
	none := DefaultEnv()
	none.Collector.Douban.CookieMode = "none"
	if errs := ValidateEnv(none); len(errs) != 0 {
		t.Errorf("cookie_mode=none 应通过: %v", errs)
	}

	fileMissing := DefaultEnv()
	fileMissing.Collector.Douban.CookieMode = "file"
	if errs := ValidateEnv(fileMissing); len(errs) == 0 {
		t.Error("file 缺 cookie_file 应失败")
	}

	cloudMissing := DefaultEnv()
	cloudMissing.Collector.Douban.CookieMode = "cookiecloud"
	if errs := ValidateEnv(cloudMissing); len(errs) == 0 {
		t.Error("cookiecloud 缺字段应失败")
	}

	rawMissing := DefaultEnv()
	rawMissing.Collector.Douban.CookieMode = "raw"
	if errs := ValidateEnv(rawMissing); len(errs) == 0 {
		t.Error("raw 缺 cookie_raw 应失败")
	}
	rawOK := DefaultEnv()
	rawOK.Collector.Douban.CookieMode = "raw"
	rawOK.Collector.Douban.CookieRaw = "dbcl2=x"
	if errs := ValidateEnv(rawOK); len(errs) != 0 {
		t.Errorf("raw 有 cookie_raw 应通过: %v", errs)
	}
}

func TestKVCookieRawRoundTrip(t *testing.T) {
	env := DefaultEnv()
	env.Collector.Douban.CookieMode = "raw"
	env.Collector.Douban.CookieRaw = "a=1; b=2"
	kv := EnvToKV(env)
	if kv["secret.collector.douban.cookie_raw"] != "a=1; b=2" {
		t.Errorf("EnvToKV cookie_raw = %q", kv["secret.collector.douban.cookie_raw"])
	}
	got := KVToEnv(kv)
	if got.Collector.Douban.CookieRaw != "a=1; b=2" || got.Collector.Douban.CookieMode != "raw" {
		t.Errorf("KVToEnv = %+v", got.Collector.Douban)
	}
}

func TestKVToEnvCookieModeNone(t *testing.T) {
	env := KVToEnv(map[string]string{})
	if env.Collector.Douban.CookieMode != "none" {
		t.Errorf("缺 key 时应为 none, got %q", env.Collector.Douban.CookieMode)
	}
	env = KVToEnv(map[string]string{"secret.collector.douban.cookie_mode": ""})
	if env.Collector.Douban.CookieMode != "none" {
		t.Errorf("空串应归一化为 none, got %q", env.Collector.Douban.CookieMode)
	}
	kv := EnvToKV(&EnvLocalConfig{})
	if kv["secret.collector.douban.cookie_mode"] != "none" {
		t.Errorf("EnvToKV 空模式应写出 none, got %q", kv["secret.collector.douban.cookie_mode"])
	}
}
