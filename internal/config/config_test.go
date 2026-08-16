package config

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

func TestDefaultValues(t *testing.T) {
	cfg := DefaultApp()
	if cfg.Server.Addr != ":7777" {
		t.Errorf("默认 Addr = %q, want :7777", cfg.Server.Addr)
	}
	if cfg.Collector.Interval != 300 {
		t.Errorf("默认 Interval = %d, want 300", cfg.Collector.Interval)
	}
	if cfg.Filter.AIEnabled == nil || !*cfg.Filter.AIEnabled {
		t.Error("默认 AIEnabled 应为 true")
	}
	if cfg.Filter.BatchSize != 20 {
		t.Errorf("默认 filter.batch_size = %d, want 20", cfg.Filter.BatchSize)
	}
	if cfg.Notifier.BatchSize != 20 {
		t.Errorf("默认 notifier.batch_size = %d, want 20", cfg.Notifier.BatchSize)
	}
	if cfg.Log.MemoryLines != DefaultLogMemoryLines {
		t.Errorf("默认 log.memory_lines = %d, want %d", cfg.Log.MemoryLines, DefaultLogMemoryLines)
	}
	if cfg.Collector.Douban.RangeFrom != "-10" || cfg.Collector.Douban.RangeTo != "now" {
		t.Errorf("豆瓣默认范围 = %q → %q, want -10 → now", cfg.Collector.Douban.RangeFrom, cfg.Collector.Douban.RangeTo)
	}
	if cfg.Collector.Douban.Interval != 3 {
		t.Errorf("默认豆瓣间隔 = %d, want 3", cfg.Collector.Douban.Interval)
	}
	if cfg.Collector.SourceInterval(models.SourceDouban.String()) != 300 {
		t.Errorf("豆瓣采集间隔应走全局 300，不应被请求间隔 3 覆盖")
	}
	if len(cfg.Collector.Douban.Groups) < 5 {
		t.Error("内置豆瓣小组应至少 5 个")
	}
	if cfg.Collector.Weibo.RangeFrom != "-10" {
		t.Errorf("微博默认范围 = %q, want -10", cfg.Collector.Weibo.RangeFrom)
	}
	if cfg.Collector.Weibo.Interval != 5 {
		t.Errorf("默认微博间隔 = %d, want 5", cfg.Collector.Weibo.Interval)
	}
	if n := len(HTTPURLs(cfg.Collector.Douban.Groups)); n != 7 {
		t.Errorf("内置豆瓣小组 URL = %d, want 7", n)
	}
}

func TestKVBatchSizeFallback(t *testing.T) {
	// 仅有旧 pipeline.batch_size 时，filter/notifier 应继承
	cfg := KVToApp(map[string]string{
		"pipeline.batch_size": "35",
	})
	if cfg.Filter.BatchSize != 35 || cfg.Notifier.BatchSize != 35 {
		t.Errorf("fallback batch = filter %d notifier %d, want 35", cfg.Filter.BatchSize, cfg.Notifier.BatchSize)
	}
	// 新 key 优先
	cfg = KVToApp(map[string]string{
		"pipeline.batch_size": "35",
		"filter.batch_size":   "11",
		"notifier.batch_size": "12",
	})
	if cfg.Filter.BatchSize != 11 || cfg.Notifier.BatchSize != 12 {
		t.Errorf("新 key 优先 = filter %d notifier %d", cfg.Filter.BatchSize, cfg.Notifier.BatchSize)
	}
}

func TestKVRoundTrip(t *testing.T) {
	app := DefaultApp()
	app.Server.Addr = ":9090"
	app.Log.Level = "debug"
	app.Log.MemoryLines = 2000
	disabled := false
	app.Filter.AIEnabled = &disabled
	sec := DefaultSecrets()
	sec.Filter.LLM.APIKey = "sk-test"
	sec.Notifier.Feishu.Webhook = "https://example.com/hook"

	kv := MergeKV(AppToKV(app), SecretsToKV(sec))
	gotApp := KVToApp(kv)
	gotSec := KVToSecrets(kv)

	if gotApp.Server.Addr != ":9090" {
		t.Errorf("Addr = %q, want :9090", gotApp.Server.Addr)
	}
	if gotApp.Log.Level != "debug" {
		t.Errorf("Log.Level = %q", gotApp.Log.Level)
	}
	if gotApp.Log.MemoryLines != 2000 {
		t.Errorf("Log.MemoryLines = %d", gotApp.Log.MemoryLines)
	}
	if gotApp.Filter.AIEnabled == nil || *gotApp.Filter.AIEnabled {
		t.Error("AIEnabled 应为 false")
	}
	if gotSec.Filter.LLM.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", gotSec.Filter.LLM.APIKey)
	}
}

func TestHotConfigFromDB(t *testing.T) {
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
	hc := NewHotConfig(s)
	if err := hc.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	if hc.Get().Server.Addr != ":8888" {
		t.Errorf("Addr = %q, want :8888", hc.Get().Server.Addr)
	}
}

func TestSourceIntervalOverride(t *testing.T) {
	cfg := KVToApp(map[string]string{
		"collector.interval":        "600",
		"collector.douban.interval": "300",
		"collector.sources":         "douban",
	})
	// 轮次间隔用全局；豆瓣 300 是请求间隔，不覆盖轮次
	if got := cfg.Collector.SourceInterval("douban"); got != 600 {
		t.Errorf("douban 轮次 = %d, want 600", got)
	}
	if cfg.Collector.Douban.Interval != 300 {
		t.Errorf("豆瓣请求间隔 = %d, want 300", cfg.Collector.Douban.Interval)
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
	over := DefaultApp()
	over.Log.MemoryLines = MaxLogMemoryLines + 1
	if errs := ValidateApp(over); len(errs) == 0 {
		t.Error("memory_lines 超上限应失败")
	}
}

func TestValidateSecretsCookieMode(t *testing.T) {
	if errs := ValidateSecrets(DefaultSecrets()); len(errs) != 0 {
		t.Errorf("DefaultSecrets 应通过: %v", errs)
	}
	if errs := ValidateSecrets(&Secrets{}); len(errs) != 0 {
		t.Errorf("空 CookieMode 应视为 none 通过: %v", errs)
	}
	none := DefaultSecrets()
	none.Collector.Douban.CookieMode = "none"
	if errs := ValidateSecrets(none); len(errs) != 0 {
		t.Errorf("cookie_mode=none 应通过: %v", errs)
	}

	fileGone := DefaultSecrets()
	fileGone.Collector.Douban.CookieMode = "file"
	if errs := ValidateSecrets(fileGone); len(errs) == 0 {
		t.Error("cookie_mode=file 应失败（已移除）")
	}

	cloudMissing := DefaultSecrets()
	cloudMissing.Collector.Douban.CookieMode = "cookiecloud"
	if errs := ValidateSecrets(cloudMissing); len(errs) == 0 {
		t.Error("cookiecloud 缺字段应失败")
	}

	rawMissing := DefaultSecrets()
	rawMissing.Collector.Douban.CookieMode = "raw"
	if errs := ValidateSecrets(rawMissing); len(errs) == 0 {
		t.Error("raw 缺 cookie_raw 应失败")
	}
	rawOK := DefaultSecrets()
	rawOK.Collector.Douban.CookieMode = "raw"
	rawOK.Collector.Douban.CookieRaw = "dbcl2=x"
	if errs := ValidateSecrets(rawOK); len(errs) != 0 {
		t.Errorf("raw 有 cookie_raw 应通过: %v", errs)
	}
}

func TestKVCookieRawRoundTrip(t *testing.T) {
	sec := DefaultSecrets()
	sec.Collector.Douban.CookieMode = "raw"
	sec.Collector.Douban.CookieRaw = "a=1; b=2"
	kv := SecretsToKV(sec)
	if kv["secret.collector.douban.cookie_raw"] != "a=1; b=2" {
		t.Errorf("SecretsToKV cookie_raw = %q", kv["secret.collector.douban.cookie_raw"])
	}
	got := KVToSecrets(kv)
	if got.Collector.Douban.CookieRaw != "a=1; b=2" || got.Collector.Douban.CookieMode != "raw" {
		t.Errorf("KVToSecrets = %+v", got.Collector.Douban)
	}
	sec.Collector.Weibo.CookieMode = "cookiecloud"
	sec.Collector.Weibo.CookiecloudURL = "https://cc.example"
	sec.Collector.Weibo.CookiecloudKey = "uuid"
	sec.Collector.Weibo.CookiecloudPass = "pw"
	kv = SecretsToKV(sec)
	if kv[KeyWeiboCookieMode] != "cookiecloud" || kv[KeyWeiboCookieCloudURL] != "https://cc.example" {
		t.Errorf("SecretsToKV weibo = %q %q", kv[KeyWeiboCookieMode], kv[KeyWeiboCookieCloudURL])
	}
	got = KVToSecrets(kv)
	if got.Collector.Weibo.CookieMode != "cookiecloud" || got.Collector.Weibo.CookiecloudPass != "pw" {
		t.Errorf("KVToSecrets weibo = %+v", got.Collector.Weibo)
	}
}

func TestKVToSecretsCookieModeNone(t *testing.T) {
	sec := KVToSecrets(map[string]string{})
	if sec.Collector.Douban.CookieMode != "none" {
		t.Errorf("缺 key 时应为 none, got %q", sec.Collector.Douban.CookieMode)
	}
	sec = KVToSecrets(map[string]string{"secret.collector.douban.cookie_mode": ""})
	if sec.Collector.Douban.CookieMode != "none" {
		t.Errorf("空串应归一化为 none, got %q", sec.Collector.Douban.CookieMode)
	}
	kv := SecretsToKV(&Secrets{})
	if kv["secret.collector.douban.cookie_mode"] != "none" {
		t.Errorf("SecretsToKV 空模式应写出 none, got %q", kv["secret.collector.douban.cookie_mode"])
	}
}
