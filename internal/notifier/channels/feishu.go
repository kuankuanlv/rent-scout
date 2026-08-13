package channels

import (
	"encoding/json"

	"rent-scout/internal/notifier"
)

// NewFeishuChannel 飞书：text 消息卡片
func NewFeishuChannel(webhookURL string) notifier.Channel {
	return &webhookChannel{
		name: notifier.ChannelFeishu,
		url:  webhookURL,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msg_type": "text",
				"content":  map[string]string{"text": textPayload(items)},
			})
		},
	}
}
