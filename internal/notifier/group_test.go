package notifier

import (
	"strings"
	"testing"

	"rent-scout/internal/models"
)

// 分组：按 AddressTags[0]；无 tag → 未分组；顺序稳定
func TestGroupByAddressTag(t *testing.T) {
	posts := []models.RentPost{
		{ID: 1, AddressTags: []string{"望京"}},
		{ID: 2, AddressTags: []string{"回龙观"}},
		{ID: 3, AddressTags: []string{"望京", "soho"}},
		{ID: 4, AddressTags: nil},
	}
	groups := GroupByAddressTag(posts)
	if len(groups) != 3 {
		t.Fatalf("组数: %d, want 3 (望京/回龙观/未分组)", len(groups))
	}
	if len(groups["望京"]) != 2 {
		t.Errorf("望京组: %d, want 2 (id 1,3)", len(groups["望京"]))
	}
	if groups[GroupUnknown][0].ID != 4 {
		t.Errorf("未分组: %v", groups[GroupUnknown])
	}
}

// 组内排序：AI 推荐理由充分的（Reason 非空）优先，然后按 ID
func TestGroupSorting(t *testing.T) {
	// 组装 NotifyItem 时 reason 来自 filter_results；此处验证排序函数
	items := []NotifyItem{
		{PostID: 1, Reason: "近地铁"},
		{PostID: 2},
	}
	got := sortByPriority(items)
	if got[0].PostID != 1 {
		t.Errorf("理由充分应优先: %+v", got)
	}
}

// 反馈链接：HMAC 签名格式（规格 7.1）
func TestBuildFeedbackURL(t *testing.T) {
	u := BuildFeedbackURL(123, "useful", "mysecret")
	if !strings.Contains(u, "/f?post=123") {
		t.Errorf("链接: %s", u)
	}
	if !strings.Contains(u, "action=useful") || !strings.Contains(u, "exp=") || !strings.Contains(u, "sig=") {
		t.Errorf("链接缺字段: %s", u)
	}
}

// 已处理链接：走 /h，签名载荷含 handled
func TestBuildHandledURL(t *testing.T) {
	u := BuildFeedbackURL(123, "handled", "mysecret")
	if !strings.HasPrefix(u, "/h?post=123") {
		t.Errorf("已处理链接应走 /h: %s", u)
	}
	if strings.Contains(u, "action=") {
		t.Errorf("/h 不应带 action 查询参数: %s", u)
	}
	if !strings.Contains(u, "exp=") || !strings.Contains(u, "sig=") {
		t.Errorf("已处理链接缺签名字段: %s", u)
	}
}

// 无 secret：不签名（auth_required=false 全开放场景）
func TestBuildFeedbackURLNoSecret(t *testing.T) {
	u := BuildFeedbackURL(123, "useless", "")
	if strings.Contains(u, "sig=") {
		t.Errorf("无 secret 不应签名: %s", u)
	}
	h := BuildFeedbackURL(123, "handled", "")
	if h != "/h?post=123" {
		t.Errorf("无 secret 已处理: %s", h)
	}
}
