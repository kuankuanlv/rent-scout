package notifier

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

// GroupUnknown 无地址标签帖子的分组名（调整规格 B：白名单未命中 → 未分组）
const GroupUnknown = "未分组"

// NotifyItem 单帖通知内容（规格 6.2 + 调整 B 3.3：增加 AddressTag）
type NotifyItem struct {
	PostID      int64
	Title       string
	URL         string
	Price       int
	Contact     string
	Commuting   string
	Reason      string // AI 推荐理由
	AddressTag  string // 分组主 tag（AddressTags[0]；无 tag = GroupUnknown）
	FeedbackURL string // 卡片内嵌反馈链接（HMAC 签名，规格 7.1）
}

// Channel 通知渠道接口（规格 6.2）：新增渠道 = 新增一个实现。
// Send 发送一个通知批次（一组帖子）；返回成功项 PostID 与失败项。
// 部分失败可重试（调用方按 notifications 表状态机处理）
type Channel interface {
	Name() string
	Send(ctx context.Context, items []NotifyItem) (sent []int64, failed []error, err error)
}
