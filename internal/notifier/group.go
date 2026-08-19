package notifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"rent-scout/internal/actionref"
	"rent-scout/internal/models"
)

// ManualGroupName 控制台直发用的分组名，形如 手动触发-081812:01:30
func ManualGroupName(at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return "手动触发-" + at.Format("010215:04:05")
}

// groupByLocationTag 按第一条 location 标签分组；无则未分组
func groupByLocationTag(posts []models.RentPost) map[string][]models.RentPost {
	out := make(map[string][]models.RentPost)
	for _, p := range posts {
		tag := primaryLocation(p.Tags)
		out[tag] = append(out[tag], p)
	}
	return out
}

func primaryLocation(tags []models.PostTag) string {
	for _, t := range tags {
		if t.Kind == models.TagKindLocation && t.Text != "" {
			return t.Text
		}
	}
	return GroupUnknown
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
	ref := actionref.Seal(postID, secret)
	var base string
	if action == "handled" {
		base = fmt.Sprintf("/h?p=%s", ref)
	} else {
		base = fmt.Sprintf("/f?p=%s&action=%s", ref, action)
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

// AbsActionURL 工具函数：拼装 origin 与 path
func AbsActionURL(origin, path string) string {
	if origin == "" || path == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(origin, "/") + path
}
