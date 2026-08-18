package filter

import (
	"strings"

	"rent-scout/internal/models"
)

// HardVerdict 硬编码规则链判定结果；硬规则总会定案
type HardVerdict struct {
	Passed bool
}

// EvaluateHard 只看白名单地点：命中通过并记地点；没命中就拒绝，原因写成未命中。
// 黑名单不再硬筛——「中介」会误伤「非中介」，真假中介交给 AI。
func EvaluateHard(post models.RentPost, rules []models.Rule) (v HardVerdict, tags []string, hits []models.RuleHit, rejectedBy string, err error) {
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

// splitKeywords 按逗号/顿号/换行拆分关键字列表（同条内 OR）；标点和长句不当关键词
func splitKeywords(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '，' || r == '、'
	})
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !models.IsChipText(p) {
			continue
		}
		out = append(out, p)
	}
	return out
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
