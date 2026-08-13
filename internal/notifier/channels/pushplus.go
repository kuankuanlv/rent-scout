package channels

import (
	"encoding/json"
	"fmt"

	"rent-scout/internal/notifier"
)

// NewPushplusChannel PushPlus：token + title + content（微信推送）
func NewPushplusChannel(baseURL, token string) notifier.Channel {
	if baseURL == "" {
		baseURL = "https://www.pushplus.plus/send"
	}
	return &webhookChannel{
		name: notifier.ChannelPushplus,
		url:  baseURL,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			return json.Marshal(map[string]string{
				"token":   token,
				"title":   title,
				"content": textPayload(items),
			})
		},
	}
}
