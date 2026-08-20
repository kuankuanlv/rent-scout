package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/admin/posts"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

type stubNotifyManual struct {
	ids   []int64
	group string
	err   error
}

func (s *stubNotifyManual) SendSelected(ctx context.Context, ids []int64, group string) error {
	s.ids = append([]int64(nil), ids...)
	s.group = group
	return s.err
}

func TestHandleNotifySelected(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	stub := &stubNotifyManual{}
	srv.SetNotifyManual(stub)

	form := url.Values{}
	form.Add("post_id", "11")
	form.Add("post_id", "22")
	form.Add("post_id", "11")
	req := httptest.NewRequest(http.MethodPost, "/admin/notify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	msg := loc.Query().Get("msg")
	if !strings.Contains(msg, "已发送 2 条") || !strings.Contains(msg, "手动触发-") {
		t.Errorf("msg=%q", msg)
	}
	if len(stub.ids) != 2 || stub.ids[0] != 11 || stub.ids[1] != 22 {
		t.Errorf("ids=%v, want [11 22]", stub.ids)
	}
	if !strings.HasPrefix(stub.group, "手动触发-") {
		t.Errorf("group=%q", stub.group)
	}
}

func TestHandleNotifySelectedEmpty(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	stub := &stubNotifyManual{}
	srv.SetNotifyManual(stub)

	req := httptest.NewRequest(http.MethodPost, "/admin/notify", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rec.Code)
	}
	if stub.ids != nil {
		t.Errorf("空勾选不应调用 SendSelected, ids=%v", stub.ids)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("msg") != "请先勾选帖子" {
		t.Errorf("msg=%q", loc.Query().Get("msg"))
	}
}

func TestAdminPostsOnboardWhenSourcesOff(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "还没开始采集") {
		t.Error("源全关时应提示还没开始采集")
	}
	if !strings.Contains(body, "去配置采集") {
		t.Error("应有去配置采集链接")
	}
	if !strings.Contains(body, "还没有帖子。先启用采集源并贴上 Cookie") {
		t.Error("空列表应提示先启用采集源并贴 Cookie")
	}
	if !strings.Contains(body, `id="posts-collector-onboard"`) {
		t.Error("应有帖子页采集引导横幅")
	}
	if !strings.Contains(body, `data-dismiss-key="collector-not-started"`) {
		t.Error("应有 dismiss key")
	}
	if !strings.Contains(body, "不再显示") {
		t.Error("应有不再显示按钮")
	}
	if !strings.Contains(body, "粘贴原文") {
		t.Error("应指引 Cookie 粘贴方式")
	}
}

func TestAdminPostsHasBatchSelect(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "batch-1", Title: "批量勾选",
		CollectedAt: time.Now(), Status: models.PostStatusPassed,
	}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="batch-notify-form"`) {
		t.Error("缺批量通知表单")
	}
	if !strings.Contains(body, `id="select-all-posts"`) {
		t.Error("缺本页全选")
	}
	if !strings.Contains(body, `form="batch-notify-form"`) {
		t.Error("勾选框应挂到批量表单，避免和已查看表单嵌套")
	}
	if !strings.Contains(body, "批量通知") {
		t.Error("批量通知条应常显，不要勾选后才出现")
	}
	if strings.Contains(body, `id="batch-bar" class="hidden`) {
		t.Error("批量条不应默认 hidden")
	}
	if strings.Contains(body, `id="tag-drawer"`) || strings.Contains(body, "选择标签") {
		t.Error("标签筛选不应再做成抽屉")
	}
	if !strings.Contains(body, "发送通知") {
		t.Error("缺发送通知按钮")
	}
}

func TestToggleFilterTags(t *testing.T) {
	if got := posts.ToggleFilterTags("", "望京"); got != "望京" {
		t.Errorf("空选加上望京 = %q", got)
	}
	if got := posts.ToggleFilterTags("望京", "中介"); got != "望京,中介" {
		t.Errorf("再选中介 = %q", got)
	}
	if got := posts.ToggleFilterTags("望京,中介", "望京"); got != "中介" {
		t.Errorf("取消望京 = %q", got)
	}
	if got := posts.ToggleFilterTags("中介", "中介"); got != "" {
		t.Errorf("取消最后一个应清空 = %q", got)
	}
}

func TestSplitFilterTagPreview(t *testing.T) {
	var tags []models.FilterTag
	for i := 0; i < 12; i++ {
		tags = append(tags, models.FilterTag{Text: string(rune('A' + i)), Count: 12 - i})
	}
	top, more := posts.SplitFilterTagPreview(tags, 10)
	if len(top) != 10 || len(more) != 2 {
		t.Fatalf("top=%d more=%d, want 10+2", len(top), len(more))
	}
	if top[0].Text != "A" || more[0].Text != "K" {
		t.Errorf("顺序不对 top0=%s more0=%s", top[0].Text, more[0].Text)
	}
	if !posts.FilterTagsContain(more, "K") || posts.FilterTagsContain(top, "K") {
		t.Error("K 应在 more")
	}
}
