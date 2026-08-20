package port

import "context"

// 渠道标识（规格 6.3 v1 清单）
const (
	ChannelFeishu     = "feishu"
	ChannelDingtalk   = "dingtalk"
	ChannelWecom      = "wecom"
	ChannelPushplus   = "pushplus"
	ChannelServerchan = "serverchan"
	ChannelWebhook    = "webhook"
)

// NotifyItem 单帖通知内容（Spec 09 §3.2：content + actions）
type NotifyItem struct {
	PostID             int64
	Title              string
	URL                string
	Price              int
	Contact            string
	Commuting          string
	Reason             string // AI 推荐理由
	Passed             bool   // AI 审核通过/拒绝
	Layout             string // 户型（AI 抽取）
	RentType           string // 整租/合租（AI 抽取）
	Floor              string // 楼层（AI 抽取）
	Area               string // 面积（AI 抽取）
	AddressTag         string // 分组主 tag；无 tag = 未分组
	FeedbackURL        string
	FeedbackUselessURL string
	HandledURL         string
}

// Channel 通知渠道接口（规格 6.2）
type Channel interface {
	Name() string
	Send(ctx context.Context, items []NotifyItem) (sent []int64, failed []error, err error)
}
