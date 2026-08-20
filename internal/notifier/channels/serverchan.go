package channels

import (
	"encoding/json"
	"fmt"

	"rent-scout/internal/notifier/port"
)

// NewServerchanChannel Server酱：sendkey 拼 URL + title/desp（微信推送）
func NewServerchanChannel(sendkey string) port.Channel {
	url := fmt.Sprintf("https://sctapi.ftqq.com/%s.send", sendkey)
	return &webhookChannel{
		name: port.ChannelServerchan,
		url:  url,
		build: func(items []port.NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			return json.Marshal(map[string]string{
				"title": title,
				"desp":  textPayload(items),
			})
		},
	}
}
