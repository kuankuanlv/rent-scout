package filter

import (
	"testing"

	"rent-scout/internal/models"
)

func TestSystemTagsFromHardSkipsLongReason(t *testing.T) {
	long := "中介不能作为黑名单，因为原帖中通常都是“非中介”"
	got := SystemTagsFromHard(models.FilterResult{
		Status:    models.PostStatusRejected,
		HardRules: []models.RuleHit{{RuleID: 1, Reason: long}},
	}, nil)
	if len(got) != 0 {
		t.Errorf("长句不应写成标签: %+v", got)
	}
	got = SystemTagsFromHard(models.FilterResult{
		Status:    models.PostStatusRejected,
		HardRules: []models.RuleHit{{RuleID: 1, Reason: "中介"}},
	}, nil)
	if len(got) != 1 || got[0].Kind != models.TagKindBlock || got[0].Text != "中介" {
		t.Errorf("短拉黑词应写成标签: %+v", got)
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
