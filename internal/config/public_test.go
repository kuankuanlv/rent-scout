package config

import (
	"strings"
	"testing"
)

func TestResolvePublicOriginConfigured(t *testing.T) {
	got := ResolvePublicOrigin(&AppConfig{Server: ServerConfig{Addr: ":7777", PublicBase: "192.168.1.8:7777"}})
	if got != "http://192.168.1.8:7777" {
		t.Errorf("got %q", got)
	}
	got = ResolvePublicOrigin(&AppConfig{Server: ServerConfig{PublicBase: "https://scout.example/"}})
	if got != "https://scout.example" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePublicOriginFromListenHost(t *testing.T) {
	got := ResolvePublicOrigin(&AppConfig{Server: ServerConfig{Addr: "10.0.0.9:9000"}})
	if got != "http://10.0.0.9:9000" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePublicOriginAutoLAN(t *testing.T) {
	got := ResolvePublicOrigin(&AppConfig{Server: ServerConfig{Addr: ":7777"}})
	if !strings.HasPrefix(got, "http://") {
		t.Errorf("got %q", got)
	}
	if !strings.HasSuffix(got, ":7777") {
		t.Errorf("端口应对齐监听: %q", got)
	}
	if strings.Contains(got, "0.0.0.0") {
		t.Errorf("不应把 0.0.0.0 写进链接: %q", got)
	}
}
