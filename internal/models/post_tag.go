package models

import "time"

// 帖子标签 kind / source（post_tags 表）
const (
	TagKindLocation  = "location"
	TagKindBlock     = "block"
	TagKindUnmatched = "unmatched"
	TagKindFeedback  = "feedback"
	TagKindManual    = "manual"

	TagSourceSystem = "system"
	TagSourceUser   = "user"
)

// PostTag 帖子标签行（展示、筛选、分组统一读此表）
type PostTag struct {
	ID        int64     `json:"id,omitempty"`
	PostID    int64     `json:"postId,omitempty"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// FeedbackTagText 有用/无用动作对应的标签文案
func FeedbackTagText(action string) string {
	if action == FeedbackUseless {
		return "无用"
	}
	return "有用"
}
