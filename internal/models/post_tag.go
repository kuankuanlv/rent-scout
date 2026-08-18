package models

import (
	"strings"
	"time"
	"unicode"
)

// 帖子标签 kind / source（post_tags 表）
const (
	TagKindLocation  = "location"
	TagKindBlock     = "block"
	TagKindUnmatched = "unmatched"
	TagKindFeedback  = "feedback"
	TagKindManual    = "manual"

	TagSourceSystem = "system"
	TagSourceUser   = "user"

	// MaxChipTagRunes 标签列/筛选项上限；再长就是句子（AI 理由、人工备注），不进标签
	MaxChipTagRunes = 20
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

// IsChipKind 能出现在全览标签列和筛选项的 kind
func IsChipKind(kind string) bool {
	switch kind {
	case TagKindLocation, TagKindBlock, TagKindUnmatched, TagKindFeedback:
		return true
	default:
		return false
	}
}

// IsChipText 关键字够短、不全是标点，才能当标签；句子留给 AI 原因列
func IsChipText(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	n := 0
	has := false
	for _, r := range s {
		n++
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			has = true
		}
	}
	return has && n <= MaxChipTagRunes
}

// FilterTag 全览筛选项：文案 + 出现帖数
type FilterTag struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// SplitFilterTags 筛选条多选：按逗号拆开，丢掉不合格 chip，保序去重
func SplitFilterTags(csv string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if !IsChipText(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ChipTags 全览标签列：地点/拉黑词/未命中/有用无用；人工备注和 AI 理由去掉
func ChipTags(tags []PostTag, aiReason string) []PostTag {
	aiReason = strings.TrimSpace(aiReason)
	out := make([]PostTag, 0, len(tags))
	for _, t := range tags {
		if !IsChipKind(t.Kind) || !IsChipText(t.Text) {
			continue
		}
		if aiReason != "" && t.Text == aiReason {
			continue
		}
		out = append(out, t)
	}
	return out
}
