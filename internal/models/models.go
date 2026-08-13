package models

import "time"

// 帖子主状态：仅四态 collected|pending|passed|rejected（Spec 09 §1）
// 渠道是否发出 → notifications；有用/无用 → feedbacks；运营已处理 → handled_at；禁止写 sent/acked。
const (
	PostStatusCollected = "collected" // 已采集入库，待筛选
	PostStatusPending   = "pending"   // 筛选处理中/瞬时失败待重试
	PostStatusPassed    = "passed"    // 筛选通过，待通知
	PostStatusRejected  = "rejected"  // 筛选拒绝
)

// 筛选阶段（FilterResult.Stage）
const (
	StageHardRule = "hard_rule" // 硬编码规则链
	StageAIRule   = "ai_rule"   // AI 自然语言规则链
)

// 规则类型（Rule.Type）：仅 whitelist|blacklist|ai_natural（Spec 09 §2）
const (
	RuleTypeWhitelist = "whitelist"  // 白名单：命中通过并写入 address_tags
	RuleTypeBlacklist = "blacklist"  // 黑名单：命中拒绝
	RuleTypeAINatural = "ai_natural" // 自然语言（LLM）
)

// 通知状态（Notification.Status）
const (
	NotifyStatusPending = "pending" // 待发送
	NotifyStatusSent    = "sent"    // 已发送
	NotifyStatusFailed  = "failed"  // 失败可重试
	NotifyStatusDead    = "dead"    // 死信（超阈值）
)

// 反馈动作（Feedback.Action）
const (
	FeedbackUseful  = "useful"
	FeedbackUseless = "useless"
)

// RentPost 归一化房源帖子（规格 3.1，collector 的输出）
type RentPost struct {
	ID          int64  // 自增主键
	Source      string // 源标识：douban / beike / ...
	ExternalID  string // 源内唯一 ID
	URL         string // 原文链接
	Title       string
	Content     string // 原文正文（HTML 或纯文本）
	Author      string
	AuthorURL   string
	PublishedAt time.Time  // 源发布时间
	CollectedAt time.Time  // 采集时间
	Status      string     // 主状态：仅 collected|pending|passed|rejected
	AddressTags []string   `json:"addressTags"` // 地址标签（调整规格 2.3）：白名单命中地点，多值；分组主键 = [0]
	HandledAt   *time.Time // 已处理时间；nil=未处理（独立于 useful/useless 反馈）
	Raw         string     // 源适配器完整原始输出（JSON，供重放/排查）
}

// DedupKey 去重键：源 + 源内 ID（posts 唯一索引同构）
func (p RentPost) DedupKey() string {
	return p.Source + ":" + p.ExternalID
}

// RuleHit 硬编码规则命中详情（规格 3.2）
type RuleHit struct {
	RuleID int64  `json:"ruleId"`
	Mode   string `json:"mode"`   // 废弃；迁移期可空
	Reason string `json:"reason"` // 命中的关键词/匹配文本
}

// AIResult AI 判定详情（规格 3.2）
type AIResult struct {
	Passed      bool    `json:"passed"`
	Reason      string  `json:"reason"`      // 推荐/拒绝理由
	Price       int     `json:"price"`       // 月租金（AI 识别）
	Contact     string  `json:"contact"`     // 联系人信息（AI 识别）
	Commuting   string  `json:"commuting"`   // 通勤信息
	Confidence  float64 `json:"confidence"`  // 置信度
	Model       string  `json:"model"`       // 实际使用的模型
	RawResponse string  `json:"rawResponse"` // LLM 原始输出（JSON）
}

// FilterResult 筛选结果（规格 3.2，1:1 posts）
type FilterResult struct {
	PostID     int64
	Status     string // pending / passed / rejected
	Stage      string // 拒绝阶段：hard_rule / ai_rule
	RejectedBy string // 拒绝原因摘要（人类可读）
	DecidedAt  time.Time
	HardRules  []RuleHit // 硬编码规则命中详情
	AI         *AIResult // AI 判定详情（nil = 未执行）
}

// Rule 筛选规则（规格 3.3，rules 表，运行时增删改）
type Rule struct {
	ID        int64
	Name      string
	Type      string // whitelist / blacklist / ai_natural
	Mode      string // 废弃：校验忽略，迁移后可不读
	Value     string // 白/黑：逗号分隔词；ai_natural：自然语言
	Enabled   bool
	Priority  int
	CreatedAt time.Time
}

// Notification 通知记录（规格 3.4，1:N posts）
type Notification struct {
	ID        int64
	PostID    int64
	Channel   string // feishu / dingtalk / wecom / pushplus / serverchan / webhook
	Status    string // pending / sent / failed / dead
	Attempts  int
	LastError string
	SentAt    *time.Time
}

// Feedback 用户反馈（规格 3.4，自学习入口）
type Feedback struct {
	ID        int64
	PostID    int64
	Channel   string
	Action    string // useful / useless
	Reason    string // 用户填的原因/建议
	CreatedAt time.Time
}

// Cursor 源采集游标（规格 3.5 source_state，增量断点续传）
type Cursor struct {
	Source    string
	Value     string // 语义由源适配器定义（页码/时间戳/URL）
	UpdatedAt time.Time
}
