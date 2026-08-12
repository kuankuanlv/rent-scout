package notifier

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// webhookChannel 基于 webhook 的渠道：JSON 载荷 + 可选签名；Send 按组发一条
type webhookChannel struct {
	name    string
	url     string
	build   func(items []NotifyItem) ([]byte, error) // 构造载荷
	signURL func(rawURL string) (string, error)      // 可选 URL 签名（钉钉加签）
}

func (c *webhookChannel) Name() string { return c.name }

func (c *webhookChannel) Send(ctx context.Context, items []NotifyItem) ([]int64, []error, error) {
	if len(items) == 0 {
		return nil, nil, nil
	}
	body, err := c.build(items)
	if err != nil {
		return nil, nil, fmt.Errorf("%s 构造载荷: %w", c.name, err)
	}
	target := c.url
	if c.signURL != nil {
		target, err = c.signURL(c.url)
		if err != nil {
			return nil, nil, fmt.Errorf("%s 签名: %w", c.name, err)
		}
	}
	if _, err := PostJSON(ctx, target, body, 10); err != nil {
		sent := []int64{}
		failed := make([]error, len(items))
		for i, it := range items {
			failed[i] = fmt.Errorf("%s 发送失败: %w", c.name, err)
			_ = it
		}
		return sent, failed, err
	}
	sent := make([]int64, len(items))
	for i, it := range items {
		sent[i] = it.PostID
	}
	return sent, nil, nil
}

// textPayload 统一文本载荷：分组标题 + 组内帖子列表
func textPayload(items []NotifyItem) string {
	var sb strings.Builder
	group := items[0].AddressTag
	if group == "" {
		group = GroupUnknown
	}
	fmt.Fprintf(&sb, "📍 %s · %d 条新帖子\n", group, len(items))
	for _, it := range items {
		sb.WriteString("——\n")
		fmt.Fprintf(&sb, "%s\n%s\n", it.Title, it.URL)
		if it.Price > 0 {
			fmt.Fprintf(&sb, "价格: %d 元/月\n", it.Price)
		}
		if it.Commuting != "" {
			fmt.Fprintf(&sb, "通勤: %s\n", it.Commuting)
		}
		if it.Reason != "" {
			fmt.Fprintf(&sb, "推荐理由: %s\n", it.Reason)
		}
		if it.FeedbackURL != "" {
			fmt.Fprintf(&sb, "反馈: 有用 %s\n", it.FeedbackURL)
		}
		if it.FeedbackUselessURL != "" {
			fmt.Fprintf(&sb, "反馈: 无用 %s\n", it.FeedbackUselessURL)
		}
	}
	return sb.String()
}

// NewFeishuChannel 飞书：text 消息卡片
func NewFeishuChannel(webhookURL string) Channel {
	return &webhookChannel{
		name: ChannelFeishu,
		url:  webhookURL,
		build: func(items []NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msg_type": "text",
				"content":  map[string]string{"text": textPayload(items)},
			})
		},
	}
}

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
func NewDingtalkChannel(webhookURL, secret string) Channel {
	ch := &webhookChannel{
		name: ChannelDingtalk,
		url:  webhookURL,
		build: func(items []NotifyItem) ([]byte, error) {
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

// NewWecomChannel 企业微信：text 消息
func NewWecomChannel(webhookURL string) Channel {
	return &webhookChannel{
		name: ChannelWecom,
		url:  webhookURL,
		build: func(items []NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msgtype": "text",
				"text":    map[string]string{"content": textPayload(items)},
			})
		},
	}
}

// NewPushplusChannel PushPlus：token + title + content（微信推送）
func NewPushplusChannel(baseURL, token string) Channel {
	if baseURL == "" {
		baseURL = "https://www.pushplus.plus/send"
	}
	return &webhookChannel{
		name: ChannelPushplus,
		url:  baseURL,
		build: func(items []NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			return json.Marshal(map[string]string{
				"token":   token,
				"title":   title,
				"content": textPayload(items),
			})
		},
	}
}

// NewServerchanChannel Server酱：sendkey 拼 URL + title/desp（微信推送）
func NewServerchanChannel(sendkey string) Channel {
	url := fmt.Sprintf("https://sctapi.ftqq.com/%s.send", sendkey)
	return &webhookChannel{
		name: ChannelServerchan,
		url:  url,
		build: func(items []NotifyItem) ([]byte, error) {
			title := fmt.Sprintf("%s · %d 条", items[0].AddressTag, len(items))
			return json.Marshal(map[string]string{
				"title": title,
				"desp":  textPayload(items),
			})
		},
	}
}

// NewWebhookChannel 自定义 webhook：URL + 可选 JSON 模板（缺省发送全字段）
// 模板为 Go template 字符串，占位符：{{.Title}} {{.Text}} {{.Count}}（v1 简化：只支持占位替换）
func NewWebhookChannel(rawURL, template string) Channel {
	return &webhookChannel{
		name: ChannelWebhook,
		url:  rawURL,
		build: func(items []NotifyItem) ([]byte, error) {
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
