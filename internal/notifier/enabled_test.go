package notifier

import (
	"reflect"
	"testing"

	"rent-scout/internal/config"
)

// EnabledChannels 已配 webhook 的渠道名列表（规格 7.2 约定：配了即启用）
func TestEnabledChannels(t *testing.T) {
	// 全空 env → 空切片
	if got := EnabledChannels(config.EnvNotifier{}); len(got) != 0 {
		t.Fatalf("全空 env: %v, want 空切片", got)
	}

	// 仅配 feishu → [feishu]
	got := EnabledChannels(config.EnvNotifier{Feishu: config.WebhookSecretConfig{Webhook: "https://x"}})
	want := []string{ChannelFeishu}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("仅 feishu: %v, want %v", got, want)
	}

	// 配 pushplus+serverchan → 按常量序
	got = EnabledChannels(config.EnvNotifier{
		Pushplus:   config.PushplusConfig{Token: "t"},
		Serverchan: config.ServerchanConfig{Sendkey: "s"},
	})
	want = []string{ChannelPushplus, ChannelServerchan}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pushplus+serverchan: %v, want %v", got, want)
	}

	// 全配 → 六渠道常量序
	got = EnabledChannels(config.EnvNotifier{
		Feishu:     config.WebhookSecretConfig{Webhook: "f"},
		Dingtalk:   config.DingtalkConfig{Webhook: "d", Secret: "s"},
		Wecom:      config.WebhookSecretConfig{Webhook: "w"},
		Pushplus:   config.PushplusConfig{Token: "p"},
		Serverchan: config.ServerchanConfig{Sendkey: "sc"},
		Webhook:    config.CustomWebhookConfig{URL: "u"},
	})
	want = []string{ChannelFeishu, ChannelDingtalk, ChannelWecom, ChannelPushplus, ChannelServerchan, ChannelWebhook}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("全配: %v, want %v", got, want)
	}
}
