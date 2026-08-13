package channels

import (
	"encoding/json"
	"fmt"

	"rent-scout/internal/notifier"
)

// NewServerchanChannel Server酱：sendkey 拼 URL + title/desp（微信推送）
func NewServerchanChannel(sendkey string) notifier.Channel {
	url := fmt.Sprintf("https://sctapi.ftqq.com/%s.send", sendkey)
	return &webhookChannel{
		name: notifier.ChannelServerchan,
		url:  url,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			return json.Marshal(map[string]string{
				"title": title,
				"desp":  textPayload(items),
			})
		},
	}
}
