package notifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"rent-scout/internal/models"
)

// GroupByAddressTag 按分组主键（AddressTags[0]，无 tag → 未分组）分组（调整规格 B 3.1）。
// 返回 map[group][]post，组内顺序保持传入顺序（排序由调用方按 NotifyItem 执行）
func GroupByAddressTag(posts []models.RentPost) map[string][]models.RentPost {
	out := make(map[string][]models.RentPost)
	for _, p := range posts {
		tag := GroupUnknown
		if len(p.AddressTags) > 0 && p.AddressTags[0] != "" {
			tag = p.AddressTags[0]
		}
		out[tag] = append(out[tag], p)
	}
	return out
}

// sortByPriority 组内排序：AI 推荐理由充分（Reason 非空）优先，然后按 PostID（调整 B 3.1）
func sortByPriority(items []NotifyItem) []NotifyItem {
	sorted := append([]NotifyItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := sorted[i].Reason != "", sorted[j].Reason != ""
		if ri != rj {
			return ri
		}
		return sorted[i].PostID < sorted[j].PostID
	})
	return sorted
}

// BuildFeedbackURL 生成卡片内嵌动作链接（规格 7.1 / Spec 09 §3.4）：
// 反馈：/f?post=<id>&action=<useful|useless>&exp=<ts>&sig=<hmac>
// 已处理：/h?post=<id>&exp=<ts>&sig=<hmac>（签名载荷仍为 post|handled|exp）
// secret 取 admin token；secret 空 = 不签名（鉴权关闭全开放场景）
func BuildFeedbackURL(postID int64, action, secret string) string {
	var base string
	if action == "handled" {
		base = fmt.Sprintf("/h?post=%d", postID)
	} else {
		base = fmt.Sprintf("/f?post=%d&action=%s", postID, action)
	}
	if secret == "" {
		return base
	}
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	sig := hmacSHA256(secret, fmt.Sprintf("%d|%s|%d", postID, action, exp))
	return fmt.Sprintf("%s&exp=%d&sig=%s", base, exp, sig)
}

// hmacSHA256 HMAC-SHA256 十六进制签名
func hmacSHA256(secret, data string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
