package filter

import (
	"strings"
	"testing"

	"rent-scout/internal/models"
)

// 白名单命中：直接通过 + 产出 AddressTags（多值，按规则优先级降序，去重）
func TestHardWhitelistProducesTags(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "14号线", Priority: 1},
		{ID: 3, Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "望京", Priority: 5}, // 重复地点去重
	}
	post := models.RentPost{ID: 1, Title: "望京西园整租", Content: "近14号线望京站，两居4500"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed {
		t.Error("白名单命中应通过")
	}
	// 按 Priority 降序：规则1(10)先 → "望京"；规则2(1) → "14号线"；规则3(5) 重复去重
	if len(tags) != 2 || tags[0] != "望京" || tags[1] != "14号线" {
		t.Errorf("tags = %v, want [望京 14号线]", tags)
	}
}

// 黑名单命中：拒绝（白名单未命中时）
func TestHardBlacklistRejects(t *testing.T) {
	rules := []models.Rule{
		{ID: 5, Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介,代理", Priority: 10},
	}
	post := models.RentPost{ID: 2, Title: "回龙观精装", Content: "房东直租，中介勿扰，代理绕行"}
	v, _, hits, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Error("黑名单命中应拒绝")
	}
	if !strings.Contains(rejectedBy, "中介") {
		t.Errorf("拒绝原因 = %q, want 含中介", rejectedBy)
	}
	if len(hits) == 0 || hits[0].RuleID != 5 {
		t.Errorf("hits = %+v, want 规则5", hits)
	}
}

// 白名单短路优先：白名单命中时忽略黑名单（规格 5.3）
func TestHardWhitelistShortCircuit(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介", Priority: 9},
	}
	post := models.RentPost{ID: 3, Title: "望京整租", Content: "中介勿扰"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || len(tags) != 1 {
		t.Errorf("白名单应短路通过: passed=%v tags=%v", v.Passed, tags)
	}
}

// 关键词 include/exclude（无白名单/黑名单时）
func TestHardKeywordModes(t *testing.T) {
	inc := []models.Rule{{ID: 7, Type: models.RuleTypeHardKeyword, Mode: models.RuleModeInclude, Value: "整租", Priority: 1}}
	post := models.RentPost{ID: 4, Title: "两居整租", Content: "x"}
	v, _, _, _, err := EvaluateHard(post, inc)
	if err != nil || !v.Passed {
		t.Errorf("include 命中应通过: %v %v", v, err)
	}
	exc := []models.Rule{{ID: 8, Type: models.RuleTypeHardKeyword, Mode: models.RuleModeExclude, Value: "合租", Priority: 1}}
	post2 := models.RentPost{ID: 5, Title: "合租单间", Content: "x"}
	v, _, _, _, err = EvaluateHard(post2, exc)
	if err != nil || v.Passed {
		t.Errorf("exclude 命中应拒绝: %v %v", v, err)
	}
	// 无命中：include 未命中 → 未通过（待 AI/默认）；exclude 未命中 → 通过
	post3 := models.RentPost{ID: 6, Title: "普通帖", Content: "x"}
	v, _, _, _, err = EvaluateHard(post3, inc)
	if err != nil || v.Passed {
		t.Errorf("include 未命中应不通过: %v %v", v, err)
	}
	v, _, _, _, err = EvaluateHard(post3, exc)
	if err != nil || !v.Passed {
		t.Errorf("exclude 未命中应通过: %v %v", v, err)
	}
}

// 多条 exclude 规则：第一条未命中不立即通过，须全部评估（P3-2 审查发现）
func TestHardMultipleExcludeRules(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeHardKeyword, Mode: models.RuleModeExclude, Value: "合租", Priority: 10},
		{ID: 2, Type: models.RuleTypeHardKeyword, Mode: models.RuleModeExclude, Value: "中介", Priority: 5},
	}
	// 命中第二条 exclude：第一条未命中后仍评估后续 → 拒绝
	v, _, _, _, err := EvaluateHard(models.RentPost{ID: 1, Title: "中介房源", Content: "x"}, rules)
	if err != nil || v.Passed {
		t.Errorf("命中后续 exclude 应拒绝: %v %v", v, err)
	}
	// 全部 exclude 未命中 → 通过
	v, _, _, _, err = EvaluateHard(models.RentPost{ID: 2, Title: "普通整租房源", Content: "x"}, rules)
	if err != nil || !v.Passed {
		t.Errorf("全部 exclude 未命中应通过: %v %v", v, err)
	}
	// 命中第一条 exclude → 拒绝（既有行为）
	v, _, _, _, err = EvaluateHard(models.RentPost{ID: 3, Title: "合租单间", Content: "x"}, rules)
	if err != nil || v.Passed {
		t.Errorf("命中首条 exclude 应拒绝: %v %v", v, err)
	}
}
