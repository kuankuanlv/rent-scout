package channels

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/notifier"
)

// NewPushplusChannel PushPlus：token + title + content；topic 非空走一对多群组
func NewPushplusChannel(baseURL, token, topic string) notifier.Channel {
	if baseURL == "" {
		baseURL = "https://www.pushplus.plus/send"
	}
	return &webhookChannel{
		name: notifier.ChannelPushplus,
		url:  baseURL,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			body := map[string]string{
				"token":   token,
				"title":   title,
				"content": textPayload(items),
			}
			if t := strings.TrimSpace(topic); t != "" {
				body["topic"] = t
			}
			return json.Marshal(body)
		},
	}
}
