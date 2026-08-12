package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// newAdminTestStore 管理面测试用 store 实例（admin 测试需要真实 db 播种数据）
func newAdminTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestHealthzNoAuth：健康检查豁免鉴权，无 token 直接 200 "ok"
func TestHealthzNoAuth(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body.String())
	}
}

// TestAuthRequired：auth_required=true + token="t" → 无 token 401、Bearer/URL 正确 200、错 token 401。
// 目前除 healthz/metrics 外无实际路由，直接测鉴权中间件（包 200 的下游 handler）
func TestAuthRequired(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "t", nil)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := srv.auth(next)

	cases := []struct {
		name  string
		setup func(*http.Request)
		want  int
	}{
		{"无 token", func(r *http.Request) {}, http.StatusUnauthorized},
		{"Bearer 正确", func(r *http.Request) { r.Header.Set("Authorization", "Bearer t") }, http.StatusOK},
		{"URL token 正确", func(r *http.Request) { r.URL.RawQuery = "token=t" }, http.StatusOK},
		{"错 token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
			tc.setup(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	// 豁免：/healthz、/metrics 即使鉴权开启也无 token 200
	for _, path := range []string{"/healthz", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s 应豁免鉴权, status = %d, want 200", path, rec.Code)
		}
	}
}

// TestAuthOptional：auth_required=false → 任意请求放行（无 token 也 200）
func TestAuthOptional(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{}) // AuthRequired 默认 false
	srv := NewServer(s, rt, "t", nil)            // 配了 token 也不拦截

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
	rec := httptest.NewRecorder()
	srv.auth(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestMetrics：播种数据 → Prometheus 文本含预期行（计数 + 渠道维度）
func TestMetrics(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	now := time.Now()
	// 3 帖今日采集（今日统计来源）
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("m%d", i), Title: "t",
			CollectedAt: now, Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	// 1 passed + 1 rejected 今日判定
	s.SaveFilterResult(models.FilterResult{PostID: postID(t, s, "m0"), Status: models.PostStatusPassed,
		Stage: models.StageHardRule, DecidedAt: now})
	s.SaveFilterResult(models.FilterResult{PostID: postID(t, s, "m1"), Status: models.PostStatusRejected,
		Stage: models.StageHardRule, RejectedBy: "x", DecidedAt: now})
	// 通知：feishu sent ×1、pushplus dead ×1
	sent := postID(t, s, "m2")
	if _, err := s.InsertNotification(sent, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(sent, "feishu"); err != nil {
		t.Fatal(err)
	}
	dead := postID(t, s, "m0")
	if _, err := s.InsertNotification(dead, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(dead, "pushplus", "403"); err != nil {
		t.Fatal(err)
	}

	rt := config.NewRuntime(&config.AppConfig{})
	srv := NewServer(s, rt, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	for _, want := range []string{
		"# TYPE rent_scout_posts_collected_total gauge",
		"# TYPE rent_scout_posts_passed_total gauge",
		"# TYPE rent_scout_posts_rejected_total gauge",
		"# TYPE rent_scout_posts_pending_total gauge",
		"rent_scout_posts_collected_total 3",
		"rent_scout_posts_passed_total 1",
		"rent_scout_posts_rejected_total 1",
		`rent_scout_notify_sent_total{channel="feishu"} 1`,
		`rent_scout_notify_dead_total{channel="pushplus"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics 缺行 %q:\n%s", want, body)
		}
	}
}

// postID 按 external_id 反查帖子 ID（admin 包测试无法访问 store 内部字段，走公开查询）
func postID(t *testing.T, s *store.Store, externalID string) int64 {
	t.Helper()
	all, err := s.ListPosts("", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if p.ExternalID == externalID {
			return p.ID
		}
	}
	t.Fatalf("帖子 %s 未找到", externalID)
	return 0
}
