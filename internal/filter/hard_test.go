package filter

import (
	"strings"
	"testing"

	"rent-scout/internal/models"
)

// 白名单命中：直接通过 + 产出 AddressTags（多值，按规则优先级降序，去重）
func TestHardWhitelistProducesTags(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeWhitelist, Value: "14号线", Priority: 1},
		{ID: 3, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 5}, // 重复地点去重
	}
	post := models.RentPost{ID: 1, Title: "望京西园整租", Content: "近14号线望京站，两居4500"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || !v.Decided {
		t.Error("白名单命中应通过并定案")
	}
	// 按 Priority 降序：规则1(10)先 → "望京"；规则2(1) → "14号线"；规则3(5) 重复去重
	if len(tags) != 2 || tags[0] != "望京" || tags[1] != "14号线" {
		t.Errorf("tags = %v, want [望京 14号线]", tags)
	}
}

// 黑名单命中：拒绝（白名单未命中时）
func TestHardBlacklistRejects(t *testing.T) {
	rules := []models.Rule{
		{ID: 5, Type: models.RuleTypeBlacklist, Value: "中介,代理", Priority: 10},
	}
	post := models.RentPost{ID: 2, Title: "回龙观精装", Content: "房东直租，中介勿扰，代理绕行"}
	v, _, hits, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed || !v.Decided {
		t.Error("黑名单命中应拒绝并定案")
	}
	if !strings.Contains(rejectedBy, "中介") {
		t.Errorf("拒绝原因 = %q, want 含中介", rejectedBy)
	}
	if len(hits) == 0 || hits[0].RuleID != 5 {
		t.Errorf("hits = %+v, want 规则5", hits)
	}
}

// 白名单短路优先：白名单命中时忽略黑名单（Spec 09 §2.3）
func TestHardWhitelistShortCircuit(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 9},
	}
	post := models.RentPost{ID: 3, Title: "望京整租", Content: "中介勿扰"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || !v.Decided || len(tags) != 1 {
		t.Errorf("白名单应短路通过: passed=%v decided=%v tags=%v", v.Passed, v.Decided, tags)
	}
}

// 白/黑均未命中 → 未定案（交 AI）；黑名单未命中不自动通过
func TestHardBothMissUndecided(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 5},
	}
	post := models.RentPost{ID: 4, Title: "普通整租", Content: "房东直租"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v.Decided || v.Passed {
		t.Errorf("双未命中应未定案: %+v", v)
	}
	if len(tags) != 0 {
		t.Errorf("tags = %v, want 空", tags)
	}
}

// 仅黑名单且未命中 → 未定案（禁止旧「exclude 全未命中即通过」）
func TestHardBlacklistMissNotAutoPass(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeBlacklist, Value: "合租", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 5},
	}
	v, _, _, _, err := EvaluateHard(models.RentPost{ID: 2, Title: "普通整租房源", Content: "x"}, rules)
	if err != nil || v.Decided || v.Passed {
		t.Errorf("黑名单未命中应未定案: %+v %v", v, err)
	}
	// 命中任一条 → 拒绝
	v, _, _, _, err = EvaluateHard(models.RentPost{ID: 1, Title: "中介房源", Content: "x"}, rules)
	if err != nil || v.Passed || !v.Decided {
		t.Errorf("命中黑名单应拒绝: %+v %v", v, err)
	}
}
