package filter

import (
	"strings"

	"rent-scout/internal/models"
)

// HardVerdict 硬编码规则链判定结果；硬规则总会定案
type HardVerdict struct {
	Passed bool
}

// EvaluateHard 先黑后白：黑名单命中拒绝并记 tag；白名单命中通过并记地点；都未命中也拒绝，原因写成未命中。
func EvaluateHard(post models.RentPost, rules []models.Rule) (v HardVerdict, tags []string, hits []models.RuleHit, rejectedBy string, err error) {
	for _, r := range rules {
		if r.Type != models.RuleTypeBlacklist {
			continue
		}
		if kw := matchAny(post, r.Value); kw != "" {
			hits = append(hits, models.RuleHit{RuleID: r.ID, Mode: r.Mode, Reason: kw})
			return HardVerdict{Passed: false}, nil, hits, "黑名单命中:" + kw, nil
		}
	}
	whitelistHit := false
	for _, r := range rules {
		if r.Type != models.RuleTypeWhitelist {
			continue
		}
		found := matchLocations(post, r.Value)
		if len(found) == 0 {
			continue
		}
		whitelistHit = true
		tags = append(tags, found...)
		hits = append(hits, models.RuleHit{RuleID: r.ID, Mode: r.Mode, Reason: strings.Join(found, ",")})
	}
	if whitelistHit {
		return HardVerdict{Passed: true}, dedup(tags), hits, "", nil
	}
	return HardVerdict{Passed: false}, nil, nil, models.RejectedByUnmatched, nil
}

// matchLocations 匹配地点关键字（标题+正文子串，大小写不敏感），返回命中列表（按输入顺序）
func matchLocations(post models.RentPost, value string) []string {
	var found []string
	for _, loc := range splitKeywords(value) {
		// && 优先于 ||：loc 非空且（标题命中 或 正文命中）
		if loc != "" && containsFold(post.Title, loc) || loc != "" && containsFold(post.Content, loc) {
			found = append(found, loc)
		}
	}
	return found
}

// matchAny 返回第一个命中的关键字（标题+正文）
func matchAny(post models.RentPost, value string) string {
	for _, kw := range splitKeywords(value) {
		if kw == "" {
			continue
		}
		if containsFold(post.Title, kw) || containsFold(post.Content, kw) {
			return kw
		}
	}
	return ""
}

// splitKeywords 按逗号/顿号/换行拆分关键字列表（同条内 OR）
func splitKeywords(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '，' || r == '、'
	})
}

// containsFold 大小写不敏感子串匹配
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// dedup 保序去重（白名单地点多值去重）
func dedup(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
