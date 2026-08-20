package channels

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rent-scout/internal/notifier/port"
)

// dingtalkSign 钉钉加签：timestamp + HMAC-SHA256 签名（官方算法）
func dingtalkSign(secret string) (string, string, error) {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(ts + "\n" + secret)); err != nil {
		return "", "", err
	}
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return ts, url.QueryEscape(sign), nil
}

// NewDingtalkChannel 钉钉：text 消息 + 可选加签（secret 空 = 不加签）
func NewDingtalkChannel(webhookURL, secret string) port.Channel {
	ch := &webhookChannel{
		name: port.ChannelDingtalk,
		url:  webhookURL,
		build: func(items []port.NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msgtype": "text",
				"text":    map[string]string{"content": textPayload(items)},
			})
		},
	}
	if secret != "" {
		ch.signURL = func(raw string) (string, error) {
			ts, sign, err := dingtalkSign(secret)
			if err != nil {
				return "", err
			}
			sep := "?"
			if strings.Contains(raw, "?") {
				sep = "&"
			}
			return fmt.Sprintf("%s%stimestamp=%s&sign=%s", raw, sep, ts, sign), nil
		}
	}
	return ch
}
