package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/filter/ai/llm"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
	"strings"
	"testing"
	"time"
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

// newTestHotConfig 写入 setup 完成标记并加载 HotConfig
func newTestHotConfig(t *testing.T, s *store.Store, app *config.AppConfig, token string) *config.HotConfig {
	t.Helper()
	if app == nil {
		app = config.DefaultApp()
	}
	if token != "" {
		app.Admin.Token = token
	}
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	kv["setup.completed"] = "true"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	return rt
}

type testCookieProbe struct{}

func (testCookieProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (ports.CookieCloudInspect, error) {
	ins, err := cookie.InspectCookieCloudFor(ctx, draft, source)
	return ports.CookieCloudInspect{
		Cookie: ins.Cookie, Names: ins.Names, Previews: ins.Previews,
		Algo: ins.Algo, CipherField: ins.CipherField, HTTPStatus: ins.HTTPStatus, Domains: ins.Domains,
	}, err
}

func (testCookieProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) ports.DoubanPageResult {
	page := cookie.ProbePage(ctx, probeURL, rawCookie, nil)
	return ports.DoubanPageResult{OK: page.OK, HTTP: page.HTTP, Snippet: page.Snippet}
}

type testLLMProbe struct{}

func (testLLMProbe) ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error) {
	return llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true}).ListModels(ctx)
}

func (testLLMProbe) Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	return llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true}).Chat(ctx, system, user)
}

// newTestServer 创建已完成 setup 的 admin Server（含新 store）
func newTestServer(t *testing.T, app *config.AppConfig, token string, ctrl ports.SourceController) *Server {
	t.Helper()
	s := newAdminTestStore(t)
	t.Cleanup(func() { s.Close() })
	return newTestServerWithStore(t, s, app, token, ctrl)
}

// newTestServerWithStore 在已有 store 上创建 admin Server
func newTestServerWithStore(t *testing.T, s *store.Store, app *config.AppConfig, token string, ctrl ports.SourceController) *Server {
	t.Helper()
	rt := newTestHotConfig(t, s, app, token)
	srv := NewServer(s, rt, ctrl)
	srv.SetCookieProbe(testCookieProbe{})
	srv.SetLLMProbe(testLLMProbe{})
	srv.SetNotifyProbe(&stubNotifyProbe{})
	return srv
}

type stubNotifyProbe struct {
	channel string
	items   []ports.NotifyProbeItem
	err     error
}

func (s *stubNotifyProbe) Send(ctx context.Context, channel, webhook, token, topic string, items []ports.NotifyProbeItem) error {
	s.channel = channel
	s.items = items
	return s.err
}

// TestHealthzNoAuth：健康检查豁免鉴权，无 token 直接 200 "ok"
func TestHealthzNoAuth(t *testing.T) {
	srv := newTestServer(t, &config.AppConfig{}, "", nil)

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
func TestAuthRequired(t *testing.T) {
	srv := newTestServer(t, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "t", nil)

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

	// 豁免：/healthz 探活、/f /h 通知回调不带 token 也可进
	for _, path := range []string{"/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s 应豁免鉴权, status = %d, want 200", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("/metrics 属于管理数据，无凭证应重定向到登录页, status = %d, want 302", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("/admin 应重定向到登录页, status = %d", rec.Code)
	}

	// 回调不带管理 token：进 handler 后因缺参数 400，而不是 401
	for _, path := range []string{"/f", "/h"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s 不应被鉴权中间件拦, status = 401", path)
		}
	}
}

// TestAuthOptional：auth_required=false → 任意请求放行（无 token 也 200）
func TestAuthOptional(t *testing.T) {
	srv := newTestServer(t, &config.AppConfig{}, "t", nil)

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

	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
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
	all, err := s.ListPosts(store.PostListFilter{}, 100, 0)
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

func TestHTTPShutdownBudget(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := config.DefaultApp()
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	kv["setup.completed"] = "true"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	_ = rt.ReloadOnce()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	svc, err := New(Options{Config: rt, Store: s, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("HTTP 未起来: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Shutdown 超过 5s 预算")
	}
}

func TestLoginPageForgotTokenHint(t *testing.T) {
	app := config.DefaultApp()
	app.Admin.AuthRequired = true
	app.Admin.Token = "secret-tok"
	srv := newTestServer(t, app, "secret-tok", nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"忘记令牌",
		"管理台访问令牌",
		"rent-scout-*.log",
		"admin.token",
		"kv_config",
		"db/rent-scout.db",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("登录页缺 %q", want)
		}
	}
}

func TestLogsPage(t *testing.T) {
	srv := newTestServer(t, &config.AppConfig{}, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/logs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"系统日志", "/admin/logs/stream", "EventSource", "配置 → 常规"} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q", want)
		}
	}
}

func TestLogsRecentJSON(t *testing.T) {
	pkglog.ResetHubForTest()
	srv := newTestServer(t, &config.AppConfig{}, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/logs/recent?n=20", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var out struct {
		Logs []pkglog.Line `json:"logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Logs == nil {
		t.Fatal("logs 应为数组")
	}
}

func TestLogsPageTokenPassthrough(t *testing.T) {
	srv := newTestServer(t, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/logs?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/admin/logs/stream?token=secret") {
		t.Errorf("SSE 未透传 token")
	}
}
