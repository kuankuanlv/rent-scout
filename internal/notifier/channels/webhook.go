package channels

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/notifier"
)

// webhookChannel 基于 webhook 的渠道：JSON 载荷 + 可选签名；Send 按组发一条
type webhookChannel struct {
	name    string
	url     string
	build   func(items []notifier.NotifyItem) ([]byte, error) // 构造载荷
	signURL func(rawURL string) (string, error)              // 可选 URL 签名（钉钉加签）
}

func (c *webhookChannel) Name() string { return c.name }

func (c *webhookChannel) Send(ctx context.Context, items []notifier.NotifyItem) ([]int64, []error, error) {
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
	if _, err := notifier.PostJSON(ctx, target, body, 10); err != nil {
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
func textPayload(items []notifier.NotifyItem) string {
	var sb strings.Builder
	group := items[0].AddressTag
	if group == "" {
		group = notifier.GroupUnknown
	}
	fmt.Fprintf(&sb, "📍 %s · %d 条新帖子\n", group, len(items))
	for _, it := range items {
		sb.WriteString("——\n")
		fmt.Fprintf(&sb, "%s\n%s\n", it.Title, it.URL)
		if it.Price > 0 {
			fmt.Fprintf(&sb, "价格: %d 元/月\n", it.Price)
		}
		if it.Contact != "" {
			fmt.Fprintf(&sb, "联系人: %s\n", it.Contact)
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
		if it.HandledURL != "" {
			fmt.Fprintf(&sb, "已处理: %s\n", it.HandledURL)
		}
	}
	return sb.String()
}
