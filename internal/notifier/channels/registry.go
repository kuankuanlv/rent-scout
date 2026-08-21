package channels

import (
	"rent-scout/internal/config"
	"rent-scout/internal/notifier/port"
)

// Factory 渠道工厂：从密钥配置构造渠道；密钥缺失返回 ok=false
type Factory func(secrets config.SecretsNotifier) (port.Channel, bool)

// registry 渠道注册中心：新增渠道只需在此注册，服务层/探测层无需改动
var registry = map[string]Factory{
	port.ChannelFeishu: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Feishu.Webhook == "" {
			return nil, false
		}
		return NewFeishuChannel(n.Feishu.Webhook), true
	},
	port.ChannelDingtalk: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Dingtalk.Webhook == "" {
			return nil, false
		}
		return NewDingtalkChannel(n.Dingtalk.Webhook, n.Dingtalk.Secret), true
	},
	port.ChannelWecom: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Wecom.Webhook == "" {
			return nil, false
		}
		return NewWecomChannel(n.Wecom.Webhook), true
	},
	port.ChannelPushplus: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Pushplus.Token == "" {
			return nil, false
		}
		return NewPushplusChannel("", n.Pushplus.Token, n.Pushplus.Topic), true
	},
	port.ChannelServerchan: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Serverchan.Sendkey == "" {
			return nil, false
		}
		return NewServerchanChannel(n.Serverchan.Sendkey), true
	},
	port.ChannelWebhook: func(n config.SecretsNotifier) (port.Channel, bool) {
		if n.Webhook.URL == "" {
			return nil, false
		}
		return NewWebhookChannel(n.Webhook.URL, n.Webhook.Template), true
	},
}

// Names 所有注册渠道名（固定顺序，用于开关摘要）
func Names() []string {
	return []string{
		port.ChannelFeishu, port.ChannelDingtalk, port.ChannelWecom,
		port.ChannelPushplus, port.ChannelServerchan, port.ChannelWebhook,
	}
}

// Live 按配置勾选顺序返回启用渠道（勾选且密钥非空）
func Live(app *config.AppConfig, env *config.Secrets) []port.Channel {
	if app == nil || env == nil {
		return nil
	}
	var chs []port.Channel
	for _, name := range app.Notifier.Channels {
		if f, ok := registry[name]; ok {
			if ch, ok := f(env.Notifier); ok {
				chs = append(chs, ch)
			}
		}
	}
	return chs
}

// Enabled 某渠道是否启用（勾选且密钥非空）
func Enabled(app *config.AppConfig, env *config.Secrets, name string) bool {
	if app == nil || env == nil {
		return false
	}
	f, ok := registry[name]
	if !ok {
		return false
	}
	for _, n := range app.Notifier.Channels {
		if n == name {
			_, ok := f(env.Notifier)
			return ok
		}
	}
	return false
}

// Build 按渠道名从密钥构造（探测层用）；未注册或密钥缺失返回 nil
func Build(name string, secrets config.SecretsNotifier) port.Channel {
	if f, ok := registry[name]; ok {
		if ch, ok := f(secrets); ok {
			return ch
		}
	}
	return nil
}
