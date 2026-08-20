package channels

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/notifier/port"
)

// NewWebhookChannel 自定义 webhook：URL + 可选 JSON 模板（缺省发送全字段）
// 模板为 Go template 字符串，占位符：{{.Title}} {{.Text}} {{.Count}}（v1 简化：只支持占位替换）
func NewWebhookChannel(rawURL, template string) port.Channel {
	return &webhookChannel{
		name: port.ChannelWebhook,
		url:  rawURL,
		build: func(items []port.NotifyItem) ([]byte, error) {
			text := textPayload(items)
			if strings.TrimSpace(template) == "" {
				return json.Marshal(map[string]string{"text": text})
			}
			t := template
			t = strings.ReplaceAll(t, "{{.Title}}", items[0].AddressTag)
			t = strings.ReplaceAll(t, "{{.Text}}", text)
			t = strings.ReplaceAll(t, "{{.Count}}", fmt.Sprintf("%d", len(items)))
			return []byte(t), nil
		},
	}
}
