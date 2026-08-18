package models

import "testing"

func TestIsChipText(t *testing.T) {
	ok := []string{"望京", "14号线", "中介", "有用", "未命中"}
	for _, s := range ok {
		if !IsChipText(s) {
			t.Errorf("IsChipText(%q) = false, want true", s)
		}
	}
	bad := []string{"", ",,,", "...", "   ", "中介不能作为黑名单，因为原帖中通常都是“非中介”"}
	for _, s := range bad {
		if IsChipText(s) {
			t.Errorf("IsChipText(%q) = true, want false", s)
		}
	}
}

func TestChipTagsHidesManualAndAIReason(t *testing.T) {
	ai := "中介不能作为黑名单，因为原帖中通常都是“非中介”"
	in := []PostTag{
		{Kind: TagKindLocation, Text: "望京"},
		{Kind: TagKindBlock, Text: ",,,"},
		{Kind: TagKindManual, Text: "价格虚假"},
		{Kind: TagKindBlock, Text: ai},
		{Kind: TagKindFeedback, Text: "无用"},
	}
	got := ChipTags(in, ai)
	if len(got) != 2 || got[0].Text != "望京" || got[1].Text != "无用" {
		t.Errorf("ChipTags = %+v, want 望京+无用", got)
	}
}

func TestSplitFilterTags(t *testing.T) {
	got := SplitFilterTags("望京, 中介,,望京,")
	if len(got) != 2 || got[0] != "望京" || got[1] != "中介" {
		t.Errorf("SplitFilterTags = %v, want [望京 中介]", got)
	}
	if got := SplitFilterTags(" , , "); len(got) != 0 {
		t.Errorf("空白应拆成空, got %v", got)
	}
}
