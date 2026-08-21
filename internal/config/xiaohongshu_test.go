package config

import (
	"slices"
	"testing"

	"rent-scout/internal/models"
)

func TestXiaohongshuConfigRoundTrip(t *testing.T) {
	cfg := DefaultApp()
	cfg.Collector.Xiaohongshu.Searches = []string{"北京 租房"}
	cfg.Collector.Xiaohongshu.Interval = 600
	cfg.Collector.Xiaohongshu.RangeFrom = "-10"
	got := KVToApp(AppToKV(cfg))
	if got.Collector.Xiaohongshu.Searches[0] != "北京 租房" {
		t.Fatalf("searches = %v", got.Collector.Xiaohongshu.Searches)
	}
	if got.Collector.Xiaohongshu.Interval != 600 {
		t.Fatalf("interval = %d", got.Collector.Xiaohongshu.Interval)
	}
	if slices.Contains(got.Collector.Sources, models.SourceXiaohongshu.String()) {
		t.Fatal("小红书不应默认启用")
	}
}

func TestXiaohongshuConfigRoundTripFull(t *testing.T) {
	cfg := DefaultApp()
	cfg.Collector.Xiaohongshu.Searches = []string{"北京 租房", "望京"}
	cfg.Collector.Xiaohongshu.Topics = []string{"租房"}
	cfg.Collector.Xiaohongshu.Users = []string{"user1"}
	cfg.Collector.Xiaohongshu.Interval = 900
	cfg.Collector.Xiaohongshu.RangeFrom = "-30"
	cfg.Collector.Xiaohongshu.MaxPages = 8
	got := KVToApp(AppToKV(cfg))
	if len(got.Collector.Xiaohongshu.Searches) != 2 || got.Collector.Xiaohongshu.Searches[1] != "望京" {
		t.Fatalf("searches = %v", got.Collector.Xiaohongshu.Searches)
	}
	if len(got.Collector.Xiaohongshu.Topics) != 1 || got.Collector.Xiaohongshu.Topics[0] != "租房" {
		t.Fatalf("topics = %v", got.Collector.Xiaohongshu.Topics)
	}
	if len(got.Collector.Xiaohongshu.Users) != 1 || got.Collector.Xiaohongshu.Users[0] != "user1" {
		t.Fatalf("users = %v", got.Collector.Xiaohongshu.Users)
	}
	if got.Collector.Xiaohongshu.Interval != 900 {
		t.Fatalf("interval = %d", got.Collector.Xiaohongshu.Interval)
	}
	if got.Collector.Xiaohongshu.RangeFrom != "-30" {
		t.Fatalf("range_from = %q", got.Collector.Xiaohongshu.RangeFrom)
	}
	if got.Collector.Xiaohongshu.MaxPages != 8 {
		t.Fatalf("max_pages = %d", got.Collector.Xiaohongshu.MaxPages)
	}
}

func TestXiaohongshuDefaults(t *testing.T) {
	cfg := DefaultApp()
	if cfg.Collector.Xiaohongshu.Interval != 600 {
		t.Errorf("默认小红书间隔 = %d, want 600", cfg.Collector.Xiaohongshu.Interval)
	}
	if cfg.Collector.Xiaohongshu.RangeFrom != "-10" {
		t.Errorf("默认小红书范围 = %q, want -10", cfg.Collector.Xiaohongshu.RangeFrom)
	}
	if cfg.Collector.Xiaohongshu.MaxPages != 5 {
		t.Errorf("默认小红书 max_pages = %d, want 5", cfg.Collector.Xiaohongshu.MaxPages)
	}
	if slices.Contains(cfg.Collector.Sources, models.SourceXiaohongshu.String()) {
		t.Fatal("小红书不应默认加入 collector.sources")
	}
}

func TestXiaohongshuCookieSource(t *testing.T) {
	if got := CookieSource("xiaohongshu"); got != "xiaohongshu" {
		t.Errorf("CookieSource(xiaohongshu) = %q, want xiaohongshu", got)
	}
	if got := CookieCloudDomain("xiaohongshu"); got != "xiaohongshu.com" {
		t.Errorf("CookieCloudDomain(xiaohongshu) = %q, want xiaohongshu.com", got)
	}
}
