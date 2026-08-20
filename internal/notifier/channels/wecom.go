package channels

import (
	"encoding/json"

	"rent-scout/internal/notifier/port"
)

// NewWecomChannel 企业微信：text 消息
func NewWecomChannel(webhookURL string) port.Channel {
	return &webhookChannel{
		name: port.ChannelWecom,
		url:  webhookURL,
		build: func(items []port.NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msgtype": "text",
				"text":    map[string]string{"content": textPayload(items)},
			})
		},
	}
}
