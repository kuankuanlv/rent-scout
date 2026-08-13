package notifier

import "rent-scout/internal/config"

// EnabledChannels 已配 webhook 的渠道名列表（规格 7.2 约定：配了即启用）
func EnabledChannels(env config.SecretsNotifier) []string {
	var names []string
	if env.Feishu.Webhook != "" {
		names = append(names, ChannelFeishu)
	}
	if env.Dingtalk.Webhook != "" {
		names = append(names, ChannelDingtalk)
	}
	if env.Wecom.Webhook != "" {
		names = append(names, ChannelWecom)
	}
	if env.Pushplus.Token != "" {
		names = append(names, ChannelPushplus)
	}
	if env.Serverchan.Sendkey != "" {
		names = append(names, ChannelServerchan)
	}
	if env.Webhook.URL != "" {
		names = append(names, ChannelWebhook)
	}
	return names
}
