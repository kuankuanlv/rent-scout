package filter

import (
	"strings"

	"rent-scout/internal/models"
)

// HardVerdict 硬编码规则链判定结果
type HardVerdict struct {
	Passed  bool // 是否通过
	Decided bool // 本阶段是否给出最终判定（true=无需再走 AI）
}

// EvaluateHard 硬编码规则链（Spec 09 §2.3）：
//
//	白名单（命中 → Decided+Passed 短路，产出 AddressTags）→ 黑名单（命中 → Decided+Rejected）→ 未定案交 AI
//
// 黑名单未命中不自动通过。返回：判定结果、命中的地点标签（白名单）、命中详情、拒绝原因
func EvaluateHard(post models.RentPost, rules []models.Rule) (v HardVerdict, tags []string, hits []models.RuleHit, rejectedBy string, err error) {
	// ① 白名单：评估全部；命中词写入 tags；任一命中 → 短路通过
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
		return HardVerdict{Passed: true, Decided: true}, dedup(tags), hits, "", nil
	}
	// ② 黑名单：任一命中 → 拒绝；未命中不自动通过
	for _, r := range rules {
		if r.Type != models.RuleTypeBlacklist {
			continue
		}
		if kw := matchAny(post, r.Value); kw != "" {
			hits = append(hits, models.RuleHit{RuleID: r.ID, Mode: r.Mode, Reason: kw})
			return HardVerdict{Passed: false, Decided: true}, nil, hits, "黑名单命中:" + kw, nil
		}
	}
	// ③ 未定案 → 交 AI（或 AI 关闭时的默认策略）
	return HardVerdict{Passed: false, Decided: false}, nil, hits, "", nil
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

// dedup 保序去重（AddressTags 多值去重，调整规格 2.3）
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
