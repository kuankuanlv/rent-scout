package rule

import (
	"testing"

	"rent-scout/internal/models"
)

func TestHardWhitelistProducesTags(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeWhitelist, Value: "14号线", Priority: 1},
		{ID: 3, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 5},
	}
	post := models.RentPost{ID: 1, Title: "望京西园整租", Content: "近14号线望京站，两居4500"}
	v, tags, _, _, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed {
		t.Error("白名单命中应通过")
	}
	if len(tags) != 2 || tags[0] != "望京" || tags[1] != "14号线" {
		t.Errorf("tags = %v, want [望京 14号线]", tags)
	}
}

func TestHardBlacklistDoesNotReject(t *testing.T) {
	rules := []models.Rule{
		{ID: 5, Type: models.RuleTypeBlacklist, Value: "中介,代理", Priority: 10},
	}
	post := models.RentPost{ID: 2, Title: "回龙观精装", Content: "房东直租，中介勿扰，代理绕行"}
	v, tags, hits, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Error("没有白名单命中应拒绝")
	}
	if rejectedBy != models.RejectedByUnmatched {
		t.Errorf("拒绝原因 = %q, want 未命中", rejectedBy)
	}
	if len(tags) != 0 || len(hits) != 0 {
		t.Errorf("未命中不应记地点/规则命中: tags=%v hits=%v", tags, hits)
	}
}

func TestHardFeiZhongjieStillPassesWhitelist(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 9},
	}
	post := models.RentPost{ID: 3, Title: "望京整租", Content: "非中介，中介勿扰"}
	v, tags, _, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || len(tags) != 1 || tags[0] != "望京" {
		t.Errorf("白名单地点应通过，不要被中介子串误伤: passed=%v tags=%v rejectedBy=%q", v.Passed, tags, rejectedBy)
	}
}

func TestHardBothMissRejects(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeWhitelist, Value: "望京", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 5},
	}
	post := models.RentPost{ID: 4, Title: "普通整租", Content: "房东直租"}
	v, tags, hits, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed {
		t.Errorf("双未命中应拒绝: %+v", v)
	}
	if rejectedBy != models.RejectedByUnmatched {
		t.Errorf("rejectedBy = %q, want 未命中", rejectedBy)
	}
	if len(tags) != 0 || len(hits) != 0 {
		t.Errorf("未命中不应记地点/规则命中: tags=%v hits=%v", tags, hits)
	}
}

func TestHardBlacklistOnlyStillUnmatched(t *testing.T) {
	rules := []models.Rule{
		{ID: 1, Type: models.RuleTypeBlacklist, Value: "合租", Priority: 10},
		{ID: 2, Type: models.RuleTypeBlacklist, Value: "中介", Priority: 5},
	}
	v, _, _, rejectedBy, err := EvaluateHard(models.RentPost{ID: 2, Title: "普通整租房源", Content: "x"}, rules)
	if err != nil || v.Passed || rejectedBy != models.RejectedByUnmatched {
		t.Errorf("没有白名单应未命中: %+v %q %v", v, rejectedBy, err)
	}
	v, _, _, rejectedBy, err = EvaluateHard(models.RentPost{ID: 1, Title: "中介房源", Content: "x"}, rules)
	if err != nil || v.Passed || rejectedBy != models.RejectedByUnmatched {
		t.Errorf("含中介也不该当黑名单硬拒: %+v %q %v", v, rejectedBy, err)
	}
}

func TestSplitKeywordsSkipsPunctuation(t *testing.T) {
	got := splitKeywords(",,,")
	if len(got) != 0 {
		t.Errorf("splitKeywords(,,,) = %v, want 空", got)
	}
	got = splitKeywords("望京, 14号线")
	if len(got) != 2 || got[0] != "望京" || got[1] != "14号线" {
		t.Errorf("splitKeywords = %v, want [望京 14号线]", got)
	}
	got = splitKeywords("中介不能作为黑名单因为原帖中通常都是“非中介”")
	if len(got) != 0 {
		t.Errorf("长句不应当关键词: %v", got)
	}
}
