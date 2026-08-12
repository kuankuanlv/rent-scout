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

// TestAdminMarkMethodNotAllowed GET /admin/mark 必须 405 且不写库：
// 钉死「GET 链接触发写库」漏洞（mux 不限方法 + FormValue 并入 query）。
func TestAdminMarkMethodNotAllowed(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "getmark", Title: "GET 标记", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "getmark")

	// 模拟 <a>/<img> 链接触发：GET + query 携带全部写库参数
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/mark?post_id=%d&action=useful&reason=x", id), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /admin/mark status = %d, want 405", rec.Code)
	}
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("GET 触发了写库：DB 反馈 = %+v, want 0 条", items)
	}
}

// TestAdminMarkInvalidPostID post_id=0 → 400（审查点名未测分支）
func TestAdminMarkInvalidPostID(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)

	form := url.Values{"post_id": {"0"}, "action": {models.FeedbackUseful}}
	req := httptest.NewRequest(http.MethodPost, "/admin/mark", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("post_id=0 status = %d, want 400", rec.Code)
	}
}

// TestAdminTokenPropagation 鉴权开启 + ?token=secret：
// 页面 nav/筛选链接与表单 action 均透传 token（不 401）；无 token 访问 401（对照鉴权生效）。
func TestAdminTokenPropagation(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "tok1", Title: "鉴权帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "tok1")

	// 无 token → 401（对照：鉴权确实生效）
	req0 := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusUnauthorized {
		t.Errorf("GET /admin 无 token status = %d, want 401", rec0.Code)
	}

	// GET /admin?token=secret → 200 且链接透传 token
	req := httptest.NewRequest(http.MethodGet, "/admin?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin?token=secret status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"/admin?token=secret",               // nav 帖子链接
		"/admin/rules?token=secret",         // nav 规则链接
		"/admin/stats?token=secret",         // nav 统计链接
		"/admin?status=passed&token=secret", // 筛选链接（已有 query，用 & 拼接）
		"/admin/mark?token=secret",          // 表单 action
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q（token 未透传）", want)
		}
	}

	// POST /admin/mark?token=secret + 合法表单 → 302，且重定向带回 token（PRG 后不 401）
	form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "action": {models.FeedbackUseful}, "reason": {"鉴权下提交"}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/mark?token=secret", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Errorf("POST /admin/mark?token=secret status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/admin?token=secret" {
		t.Errorf("Location = %q, want /admin?token=secret", loc)
	}
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Reason != "鉴权下提交" {
		t.Errorf("DB 反馈 = %+v, want 1 条", items)
	}
}
