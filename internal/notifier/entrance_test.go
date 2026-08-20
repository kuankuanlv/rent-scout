package notifier

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/notifier/channels"
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
	chs := channels.Live(app, env)
	if len(chs) != 1 || chs[0].Name() != "feishu" {
		t.Fatalf("got %v", chs)
	}
}

func TestChannelSwitchSummary(t *testing.T) {
	cases := []struct {
		name string
		app  *config.AppConfig
		env  *config.Secrets
		want string
	}{
		{
			name: "全部关闭",
			app:  &config.AppConfig{Notifier: config.NotifierConfig{Channels: nil}},
			env:  &config.Secrets{},
			want: "feishu=false、dingtalk=false、wecom=false、pushplus=false、serverchan=false、webhook=false",
		},
		{
			name: "部分启用且密钥齐全",
			app:  &config.AppConfig{Notifier: config.NotifierConfig{Channels: []string{"feishu", "pushplus"}}},
			env: &config.Secrets{Notifier: config.SecretsNotifier{
				Feishu:   config.WebhookSecretConfig{Webhook: "https://x"},
				Pushplus: config.PushplusConfig{Token: "t"},
			}},
			want: "feishu=true、dingtalk=false、wecom=false、pushplus=true、serverchan=false、webhook=false",
		},
		{
			name: "勾选但密钥缺失按关闭算",
			app:  &config.AppConfig{Notifier: config.NotifierConfig{Channels: []string{"feishu", "wecom"}}},
			env:  &config.Secrets{Notifier: config.SecretsNotifier{Feishu: config.WebhookSecretConfig{Webhook: "https://x"}}},
			want: "feishu=true、dingtalk=false、wecom=false、pushplus=false、serverchan=false、webhook=false",
		},
		{
			name: "密钥齐全但未勾选按关闭算",
			app:  &config.AppConfig{Notifier: config.NotifierConfig{Channels: []string{"dingtalk"}}},
			env: &config.Secrets{Notifier: config.SecretsNotifier{
				Dingtalk: config.DingtalkConfig{Webhook: "https://x"},
				Wecom:    config.WebhookSecretConfig{Webhook: "https://w"},
			}},
			want: "feishu=false、dingtalk=true、wecom=false、pushplus=false、serverchan=false、webhook=false",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelSwitchSummary(tc.app, tc.env); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
