package group

import (
	"strings"
	"testing"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/notifier/port"
	"rent-scout/internal/security/actionref"
)

// 分组：按第一条 location 标签；无 tag → 未分组
func TestGroupByLocationTag(t *testing.T) {
	posts := []models.RentPost{
		{ID: 1, Tags: []models.PostTag{{Kind: models.TagKindLocation, Text: "望京"}}},
		{ID: 2, Tags: []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观"}}},
		{ID: 3, Tags: []models.PostTag{
			{Kind: models.TagKindLocation, Text: "望京"},
			{Kind: models.TagKindLocation, Text: "soho"},
		}},
		{ID: 4, Tags: nil},
	}
	groups := ByLocationTag(posts)
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
	items := []port.NotifyItem{
		{PostID: 1, Reason: "近地铁"},
		{PostID: 2},
	}
	got := SortByPriority(items)
	if got[0].PostID != 1 {
		t.Errorf("理由充分应优先: %+v", got)
	}
}

// 反馈链接：HMAC 签名格式（规格 7.1）
func TestBuildFeedbackURL(t *testing.T) {
	u := BuildFeedbackURL(123, "useful", "mysecret")
	token := strings.Split(strings.TrimPrefix(u, "/f?p="), "&")[0]
	if token == "123" || strings.Contains(u, "post=") {
		t.Errorf("链接不应出现明文帖子 id: %s", u)
	}
	if !strings.Contains(u, "/f?p=") || !strings.Contains(u, "action=useful") || !strings.Contains(u, "sig=") {
		t.Errorf("链接缺字段: %s", u)
	}
	id, err := actionref.Open(token, "mysecret")
	if err != nil || id != 123 {
		t.Errorf("解开引用: id=%d err=%v", id, err)
	}
}

func TestBuildHandledURL(t *testing.T) {
	u := BuildFeedbackURL(123, "handled", "mysecret")
	if !strings.HasPrefix(u, "/h?p=") {
		t.Errorf("已处理链接: %s", u)
	}
	token := strings.Split(strings.TrimPrefix(u, "/h?p="), "&")[0]
	if strings.Contains(token, "123") || strings.Contains(u, "post=") {
		t.Errorf("已处理链接: %s", u)
	}
	if strings.Contains(u, "action=") {
		t.Errorf("/h 不应带 action 查询参数: %s", u)
	}
}

func TestAbsActionURL(t *testing.T) {
	got := AbsActionURL("http://192.168.1.8:7777", "/f?p=abc")
	if got != "http://192.168.1.8:7777/f?p=abc" {
		t.Errorf("got %q", got)
	}
	if AbsActionURL("", "/f") != "/f" {
		t.Errorf("空 origin 应保持相对路径")
	}
	if AbsActionURL("http://x", "https://already") != "https://already" {
		t.Errorf("已是绝对地址不应再拼")
	}
}

func TestBuildFeedbackURLNoSecret(t *testing.T) {
	u := BuildFeedbackURL(123, "useless", "")
	if strings.Contains(u, "sig=") || strings.Contains(u, "123") {
		t.Errorf("无 secret: %s", u)
	}
	h := BuildFeedbackURL(123, "handled", "")
	if !strings.HasPrefix(h, "/h?p=") || strings.Contains(h, "123") {
		t.Errorf("无 secret 已处理: %s", h)
	}
}

func TestManualGroupName(t *testing.T) {
	at := time.Date(2026, 8, 18, 12, 1, 30, 0, time.Local)
	got := ManualGroupName(at)
	want := "手动触发-081812:01:30"
	if got != want {
		t.Errorf("ManualGroupName = %q, want %q", got, want)
	}
}
