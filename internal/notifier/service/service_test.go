package service

import (
	"reflect"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/notifier"
)

func TestConfiguredChannelNames(t *testing.T) {
	if got := configuredChannelNames(config.SecretsNotifier{}); len(got) != 0 {
		t.Fatalf("全空 env: %v, want 空切片", got)
	}

	got := configuredChannelNames(config.SecretsNotifier{Feishu: config.WebhookSecretConfig{Webhook: "https://x"}})
	want := []string{notifier.ChannelFeishu}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("仅 feishu: %v, want %v", got, want)
	}

	got = configuredChannelNames(config.SecretsNotifier{
		Pushplus:   config.PushplusConfig{Token: "t"},
		Serverchan: config.ServerchanConfig{Sendkey: "s"},
	})
	want = []string{notifier.ChannelPushplus, notifier.ChannelServerchan}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pushplus+serverchan: %v, want %v", got, want)
	}

	got = configuredChannelNames(config.SecretsNotifier{
		Feishu:     config.WebhookSecretConfig{Webhook: "f"},
		Dingtalk:   config.DingtalkConfig{Webhook: "d", Secret: "s"},
		Wecom:      config.WebhookSecretConfig{Webhook: "w"},
		Pushplus:   config.PushplusConfig{Token: "p"},
		Serverchan: config.ServerchanConfig{Sendkey: "sc"},
		Webhook:    config.CustomWebhookConfig{URL: "u"},
	})
	want = []string{notifier.ChannelFeishu, notifier.ChannelDingtalk, notifier.ChannelWecom, notifier.ChannelPushplus, notifier.ChannelServerchan, notifier.ChannelWebhook}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("全配: %v, want %v", got, want)
	}
}

func TestBuildChannelsSkipsMissingCreds(t *testing.T) {
	chs := buildChannels([]string{notifier.ChannelFeishu, notifier.ChannelWecom}, config.SecretsNotifier{
		Feishu: config.WebhookSecretConfig{Webhook: "https://x"},
	})
	if len(chs) != 1 || chs[0].Name() != notifier.ChannelFeishu {
		t.Fatalf("got %v", chs)
	}
}

func TestNewWithoutChannels(t *testing.T) {
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Notifier: config.NotifierConfig{Channels: []string{notifier.ChannelFeishu}},
	}, &config.Secrets{})
	svc, err := New(Options{Config: rt, Store: nil})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Enabled() {
		t.Fatal("无有效渠道不应启动")
	}
}
