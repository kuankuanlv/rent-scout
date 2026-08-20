package tag

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
	if len(got) != 1 || got[0].Kind != models.TagKindUnmatched || got[0].Text != models.RejectedByUnmatched {
		t.Errorf("拒绝应写成未命中: %+v", got)
	}
	got = SystemTagsFromHard(models.FilterResult{
		Status:    models.PostStatusRejected,
		HardRules: []models.RuleHit{{RuleID: 1, Reason: "中介"}},
	}, nil)
	if len(got) != 1 || got[0].Kind != models.TagKindUnmatched || got[0].Text != models.RejectedByUnmatched {
		t.Errorf("黑名单短词也不该写成标签: %+v", got)
	}
}
