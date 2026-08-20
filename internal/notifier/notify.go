package notifier

import (
	"rent-scout/internal/notifier/port"
)

// 兼容旧名：渠道契约在 port 包，避免 channels↔notifier 循环依赖
const (
	ChannelFeishu     = port.ChannelFeishu
	ChannelDingtalk   = port.ChannelDingtalk
	ChannelWecom      = port.ChannelWecom
	ChannelPushplus   = port.ChannelPushplus
	ChannelServerchan = port.ChannelServerchan
	ChannelWebhook    = port.ChannelWebhook
)

const DefaultMaxAttempts = 3

type NotifyItem = port.NotifyItem
type Channel = port.Channel
