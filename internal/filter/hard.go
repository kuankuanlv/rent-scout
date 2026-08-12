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

// EvaluateHard 硬编码规则链（规格 5.3）：
//
//	白名单（命中 → 直接 passed 短路，产出 AddressTags）→ 黑名单（命中 → rejected）→ 关键词（include/exclude）
//
// 返回：判定结果、命中的地点标签（白名单）、命中详情、拒绝原因
func EvaluateHard(post models.RentPost, rules []models.Rule) (v HardVerdict, tags []string, hits []models.RuleHit, rejectedBy string, err error) {
	// ① 白名单：评估全部白名单规则收集地点标签（多值，按传入顺序=优先级降序）；任一命中 → 短路通过
	// （白名单优先于黑名单，规格 5.3；不命中第一条就 return，否则多规则标签收集不全）
	whitelistHit := false
	for _, r := range rules {
		if r.Type != models.RuleTypeHardWhitelist {
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
	// ② 黑名单 + 关键词（仅白名单未命中时评估）
	excludeEvaluated := false // 有 exclude 关键词规则被评估且未命中（需全部检查后再定案）
	for _, r := range rules {
		if r.Type == models.RuleTypeHardWhitelist {
			continue
		}
		switch r.Type {
		case models.RuleTypeHardBlacklist:
			// 黑名单：命中任意关键字 → 拒绝（短路）
			if kw := matchAny(post, r.Value); kw != "" {
				hits = append(hits, models.RuleHit{RuleID: r.ID, Mode: r.Mode, Reason: kw})
				return HardVerdict{Passed: false, Decided: true}, nil, hits, "黑名单命中:" + kw, nil
			}
		case models.RuleTypeHardKeyword:
			if kw := matchAny(post, r.Value); kw != "" {
				hits = append(hits, models.RuleHit{RuleID: r.ID, Mode: r.Mode, Reason: kw})
				if r.Mode == models.RuleModeInclude {
					return HardVerdict{Passed: true, Decided: true}, nil, hits, "", nil
				}
				// exclude 命中 → 拒绝
				return HardVerdict{Passed: false, Decided: true}, nil, hits, "关键词排除:" + kw, nil
			}
			if r.Mode == models.RuleModeExclude {
				// exclude 未命中：不立即 return——继续评估后续规则（多条 exclude 时防跳过，P3-2 审查发现）
				excludeEvaluated = true
			}
			// include 未命中 → 未定案，继续
		}
	}
	// 全部规则评估完：存在被评估且均未命中的 exclude → 明确通过（负向过滤）；否则未定案待 AI
	if excludeEvaluated {
		return HardVerdict{Passed: true, Decided: true}, nil, hits, "", nil
	}
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

// splitKeywords 按逗号/换行拆分关键字列表
func splitKeywords(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '，'
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
