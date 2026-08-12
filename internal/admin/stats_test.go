package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

// TestStatsPage 统计页：GET /admin/stats 200 含今日计数、渠道成功率、死信行
func TestStatsPage(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := NewServer(s, config.NewRuntime(&config.AppConfig{}), "", nil)

	now := time.Now()
	// 今日 2 帖采集 + 今日判定 1 passed / 1 rejected
	for i := 0; i < 2; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("st%d", i), Title: "t",
			CollectedAt: now, Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: postID(t, s, "st0"), Status: models.PostStatusPassed,
		Stage: models.StageHardRule, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: postID(t, s, "st1"), Status: models.PostStatusRejected,
		Stage: models.StageHardRule, RejectedBy: "x", DecidedAt: now}); err != nil {
		t.Fatal(err)
	}

	// 渠道：feishu sent×1 + dead×1 → 成功率 50%
	sentID := postID(t, s, "st0")
	if _, err := s.InsertNotification(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	deadID := postID(t, s, "st1")
	if _, err := s.InsertNotification(deadID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(deadID, "feishu", "403 无权限"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/stats status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"统计报表", "今日概览",
		"采集 2", "通过 1", "拒绝 1", // 今日计数
		"feishu", "50%",   // 渠道行 + 成功率
		"403 无权限", // 死信行（最后错误）
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q", want)
		}
	}
}

// TestDeadReset 死信重发：POST /admin/dead/reset → 302 + dead→pending（FetchPendingNotifications 可见）；
// 再点 → 302 + 提示"该通知非死信"渲染在统计页
func TestDeadReset(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := NewServer(s, config.NewRuntime(&config.AppConfig{}), "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "dead1", Title: "死信帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "dead1")
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(id, "feishu", "403"); err != nil {
		t.Fatal(err)
	}

	post := func() *httptest.ResponseRecorder {
		form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "channel": {"feishu"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 首次：302 回统计页 + dead→pending
	rec := post()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST reset status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/stats" {
		t.Errorf("Location = %q, want /admin/stats", loc)
	}
	pending, err := s.FetchPendingNotifications("feishu", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PostID != id || pending[0].Status != models.NotifyStatusPending {
		t.Errorf("重发后 pending = %+v, want 1 条 post_id=%d status=pending", pending, id)
	}

	// 再点：非 dead → 302 + 提示
	rec2 := post()
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("二次 reset status = %d, want 302", rec2.Code)
	}
	loc := rec2.Header().Get("Location")
	if !strings.Contains(loc, "msg=") {
		t.Fatalf("Location = %q, want 含 msg 提示", loc)
	}
	req := httptest.NewRequest(http.MethodGet, loc, nil)
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req)
	if !strings.Contains(rec3.Body.String(), "该通知非死信") {
		t.Errorf("统计页缺提示「该通知非死信」")
	}
}

// TestDeadResetInvalid 非法参数 → 400：post_id=0 / 空 channel；GET → 405（防 GET 链接触发写库）
func TestDeadResetInvalid(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := NewServer(s, config.NewRuntime(&config.AppConfig{}), "", nil)

	// GET + query 携带写库参数 → 405
	req0 := httptest.NewRequest(http.MethodGet, "/admin/dead/reset?post_id=1&channel=feishu", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET reset status = %d, want 405", rec0.Code)
	}

	// post_id=0 → 400
	form := url.Values{"post_id": {"0"}, "channel": {"feishu"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("post_id=0 status = %d, want 400", rec.Code)
	}

	// 空 channel → 400
	form2 := url.Values{"post_id": {"1"}, "channel": {""}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("空 channel status = %d, want 400", rec2.Code)
	}
}

// TestStatsTokenPropagation 鉴权开启 + ?token=secret：统计页表单 action 透传 token；
// POST 重发带 token → 302 且 Location 带回 token（PRG 后不 401）
func TestStatsTokenPropagation(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "tokdead", Title: "t", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "tokdead")
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(id, "feishu", "403"); err != nil {
		t.Fatal(err)
	}

	// 无 token → 401（对照：鉴权确实生效）
	req0 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusUnauthorized {
		t.Errorf("GET /admin/stats 无 token status = %d, want 401", rec0.Code)
	}

	// 有 token → 200 且表单 action 透传 token
	req := httptest.NewRequest(http.MethodGet, "/admin/stats?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/stats?token=secret status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"/admin/stats?token=secret",      // nav 统计链接
		"/admin/dead/reset?token=secret", // 重发表单 action
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q（token 未透传）", want)
		}
	}

	// POST 重发带 token → 302 且 Location 带回 token
	form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "channel": {"feishu"}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/dead/reset?token=secret", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Errorf("POST reset status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/admin/stats?token=secret" {
		t.Errorf("Location = %q, want /admin/stats?token=secret", loc)
	}
}