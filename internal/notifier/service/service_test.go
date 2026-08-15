package service

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestNewAlwaysHasPipe(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{}, &config.Secrets{})
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.Enabled() {
		t.Fatal("无渠道也应常驻 pipeline")
	}
}

func TestLiveChannelsSkipMissingCreds(t *testing.T) {
	app := &config.AppConfig{Notifier: config.NotifierConfig{Channels: []string{"feishu", "wecom"}}}
	env := &config.Secrets{Notifier: config.SecretsNotifier{Feishu: config.WebhookSecretConfig{Webhook: "https://x"}}}
	chs := liveChannels(app, env)
	if len(chs) != 1 || chs[0].Name() != "feishu" {
		t.Fatalf("got %v", chs)
	}
}
