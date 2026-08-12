package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

// TestAdminPage 帖子全览页：GET /admin 200 含全部标题；?status=passed 过滤只显示 passed 帖
func TestAdminPage(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)

	// 播种 3 帖：passed、rejected、collected
	for i, status := range []string{models.PostStatusPassed, models.PostStatusRejected, models.PostStatusCollected} {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("page%d", i), Title: fmt.Sprintf("标题%d", i), Status: status}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// 全览：200 + 含全部标题
	if code, body := get("/admin"); code != http.StatusOK {
		t.Errorf("GET /admin status = %d, want 200", code)
	} else {
		for _, title := range []string{"标题0", "标题1", "标题2"} {
			if !strings.Contains(body, title) {
				t.Errorf("页面缺标题 %q", title)
			}
		}
	}

	// 过滤：只含 passed 帖
	if code, body := get("/admin?status=passed"); code != http.StatusOK {
		t.Errorf("GET /admin?status=passed status = %d, want 200", code)
	} else {
		if !strings.Contains(body, "标题0") {
			t.Errorf("passed 过滤缺标题0")
		}
		if strings.Contains(body, "标题1") || strings.Contains(body, "标题2") {
			t.Errorf("passed 过滤混入非 passed 帖")
		}
	}
}

// TestAdminMark 标记反馈：POST /admin/mark 合法 → 302（PRG）+ DB 有记录；非法 action → 400
func TestAdminMark(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "mark1", Title: "标记帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "mark1")

	post := func(action string) *httptest.ResponseRecorder {
		form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "action": {action}, "reason": {"测试原因"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/mark", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 合法 → 302 重定向回 /admin（PRG 防重复提交）
	rec := post(models.FeedbackUseful)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("合法标记 status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Errorf("Location = %q, want /admin", loc)
	}
	// DB 有记录
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != models.FeedbackUseful || items[0].Reason != "测试原因" {
		t.Errorf("DB 反馈 = %+v, want 1 条 useful", items)
	}

	// 非法 action → 400
	if rec := post("bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("非法 action status = %d, want 400", rec.Code)
	}
}