package channels

import (
	"encoding/json"

	"rent-scout/internal/notifier"
)

// NewWecomChannel 企业微信：text 消息
func NewWecomChannel(webhookURL string) notifier.Channel {
	return &webhookChannel{
		name: notifier.ChannelWecom,
		url:  webhookURL,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msgtype": "text",
				"text":    map[string]string{"content": textPayload(items)},
			})
		},
	}
}
