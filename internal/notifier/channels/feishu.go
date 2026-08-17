package channels

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/notifier"
)

// NewFeishuChannel 飞书：post 富文本（可点链接，非 HTML）
func NewFeishuChannel(webhookURL string) notifier.Channel {
	return &webhookChannel{
		name: notifier.ChannelFeishu,
		url:  webhookURL,
		build: func(items []notifier.NotifyItem) ([]byte, error) {
			return json.Marshal(map[string]interface{}{
				"msg_type": "post",
				"content": map[string]interface{}{
					"post": map[string]interface{}{
						"zh_cn": feishuPost(items),
					},
				},
			})
		},
	}
}

// 飞书富文本节点：text / a
type feishuNode map[string]string

func feishuText(s string) feishuNode {
	return feishuNode{"tag": "text", "text": s}
}

func feishuLink(text, href string) feishuNode {
	return feishuNode{"tag": "a", "text": text, "href": href}
}

// feishuPost 组装 zh_cn：title + 分段 content（对齐 textPayload / HTMLView 字段）
func feishuPost(items []notifier.NotifyItem) map[string]interface{} {
	group := items[0].AddressTag
	if group == "" {
		group = notifier.GroupUnknown
	}
	var paras [][]feishuNode
	for i, it := range items {
		if i > 0 {
			paras = append(paras, []feishuNode{feishuText("——")})
		}
		paras = append(paras, []feishuNode{feishuText(it.Title)})
		if it.URL != "" {
			paras = append(paras, []feishuNode{feishuLink("打开原帖", it.URL)})
		}
		if it.Price > 0 {
			paras = append(paras, []feishuNode{feishuText(fmt.Sprintf("价格: %d 元/月", it.Price))})
		}
		if it.Contact != "" {
			paras = append(paras, []feishuNode{feishuText("联系人: " + it.Contact)})
		}
		if it.Commuting != "" {
			paras = append(paras, []feishuNode{feishuText("通勤: " + it.Commuting)})
		}
		reason := strings.TrimSpace(it.Reason)
		if reason == "" {
			reason = "暂无"
		}
		paras = append(paras, []feishuNode{feishuText("AI审核原因:" + reason)})

		var actions []feishuNode
		if it.FeedbackURL != "" {
			actions = append(actions, feishuLink("有用", it.FeedbackURL))
		}
		if it.FeedbackUselessURL != "" {
			if len(actions) > 0 {
				actions = append(actions, feishuText(" · "))
			}
			actions = append(actions, feishuLink("无用", it.FeedbackUselessURL))
		}
		if it.HandledURL != "" {
			if len(actions) > 0 {
				actions = append(actions, feishuText(" · "))
			}
			actions = append(actions, feishuLink("标记已读", it.HandledURL))
		}
		if len(actions) > 0 {
			paras = append(paras, actions)
		}
	}
	return map[string]interface{}{
		"title":   fmt.Sprintf("%s · %d 条", group, len(items)),
		"content": paras,
	}
}
