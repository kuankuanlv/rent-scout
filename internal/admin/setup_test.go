package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// newSetupInProgressServer 未完成 setup 的管理面（可预置 kv）
func newSetupInProgressServer(t *testing.T, s *store.Store, extra map[string]string) *Server {
	t.Helper()
	app := config.DefaultApp()
	app.Admin.AuthRequired = true
	app.Admin.Token = "setup-tok"
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	for k, v := range extra {
		kv[k] = v
	}
	// 未完成 setup，不写 setup.completed
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(s, rt, nil)
	srv.SetCookieProbe(testCookieProbe{})
	srv.SetLLMProbe(testLLMProbe{})
	return srv
}

// TestSetupFinishSeedsDefaultRule finish 时无启用规则 → 种子默认地点白名单
func TestSetupFinishSeedsDefaultRule(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, nil)

	form := url.Values{
		"step":   {"5"},
		"action": {"finish"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/setup?token=setup-tok", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("finish status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}

	rules, err := s.ListRules(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("启用规则 = %d, want 3", len(rules))
	}
	m, _ := store.GetConfigMap(s)
	if m[config.KeySetupCompleted] != "true" {
		t.Errorf("setup.completed = %q, want true", m[config.KeySetupCompleted])
	}
}

// TestSetupSkipLastStepFinishes 末步 skip = finish 语义
func TestSetupSkipLastStepFinishes(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, nil)

	form := url.Values{
		"step":   {"5"},
		"action": {"skip"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/setup?token=setup-tok", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("skip 末步 status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin?") {
		t.Errorf("Location = %q, want /admin?…", loc)
	}
	m, _ := store.GetConfigMap(s)
	if m[config.KeySetupCompleted] != "true" {
		t.Errorf("setup.completed = %q, want true", m[config.KeySetupCompleted])
	}
	rules, err := s.ListRules(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("启用规则 = %d, want 3", len(rules))
	}
}

// TestSetupStep3ValidateSecretsRejectsRawEmpty raw 且无 cookie_raw → 400
func TestSetupStep3ValidateSecretsRejectsRawEmpty(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, nil)

	form := url.Values{
		"step":                                {"3"},
		"action":                              {"next"},
		"collector.interval":                  {"120"},
		"secret.collector.douban.cookie_mode": {"raw"},
		"secret.collector.douban.cookie_raw":  {""},
		"collector.douban.groups":             {"https://www.douban.com/group/xxx/"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/setup?token=setup-tok", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cookie_raw") {
		t.Errorf("body 应提及 cookie_raw: %s", rec.Body.String())
	}
	m, _ := store.GetConfigMap(s)
	if m[config.KeySetupCompleted] == "true" {
		t.Error("校验失败不应标记 setup 完成")
	}
}

// TestSetupStep3KeepsStoredCookieRaw 回步骤 3 留空 raw 应沿用已存
func TestSetupStep3KeepsStoredCookieRaw(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, map[string]string{
		"secret.collector.douban.cookie_mode": "raw",
		"secret.collector.douban.cookie_raw":  "dbcl2=storedvalue123456",
	})

	form := url.Values{
		"step":                                {"3"},
		"action":                              {"next"},
		"collector.interval":                  {"180"},
		"secret.collector.douban.cookie_mode": {"raw"},
		"secret.collector.douban.cookie_raw":  {""},
		"collector.douban.groups":             {"https://www.douban.com/group/yyy/"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/setup?token=setup-tok", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	m, _ := store.GetConfigMap(s)
	if m["secret.collector.douban.cookie_raw"] != "dbcl2=storedvalue123456" {
		t.Errorf("cookie_raw = %q, want 保留原值", m["secret.collector.douban.cookie_raw"])
	}
	if m["secret.collector.douban.cookie_mode"] != "raw" {
		t.Errorf("cookie_mode = %q, want raw", m["secret.collector.douban.cookie_mode"])
	}
}

// TestSetupStep3RendersCookieModeAndHint GET 步骤 3 绑定已存 mode + raw 长度 hint
func TestSetupStep3RendersCookieModeAndHint(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	raw := "dbcl2=abcdefghijklmnopqrstuvwxyz"
	srv := newSetupInProgressServer(t, s, map[string]string{
		"secret.collector.douban.cookie_mode":     "raw",
		"secret.collector.douban.cookiecloud_url": "https://cc.example.com",
		"secret.collector.douban.cookiecloud_key": "uuid-1",
		"secret.collector.douban.cookie_raw":      raw,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/setup?step=3&token=setup-tok", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="raw" selected>`) {
		t.Errorf("cookie_mode=raw 未 selected:\n%s", body)
	}
	if strings.Contains(body, `value="file"`) || strings.Contains(body, `option value="file"`) {
		t.Error("不应再有 file 选项")
	}
	if !strings.Contains(body, `data-cookie-panel="raw"`) {
		t.Error("应有 raw 面板")
	}
	if !strings.Contains(body, `value="https://cc.example.com"`) {
		t.Error("应回显 cookiecloud_url")
	}
	if !strings.Contains(body, `value="uuid-1"`) {
		t.Error("应回显 cookiecloud_key")
	}
	if strings.Contains(body, raw) {
		t.Error("cookie_raw 不应明文回显")
	}
	hint := "已保存 · 长度 "
	if !strings.Contains(body, hint) {
		t.Errorf("应含 cookie_raw 长度 hint，body 无 %q", hint)
	}
	if !strings.Contains(body, "cookie-test-btn") {
		t.Error("步骤 3 应有测试 Cookie 按钮")
	}
}

// TestSetupAllowsCookieTestDuringSetup setup 未完成时 POST cookie/test 不重定向
func TestSetupAllowsCookieTestDuringSetup(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) DoubanPageResult {
		return DoubanPageResult{OK: false, HTTP: 200, Snippet: "有异常请求从你的 IP 发出，请 登录 使用豆瓣"}
	}})

	form := url.Values{"cookie_mode": {"none"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("setup 期间 cookie/test 被重定向到 %s", rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("应返回探测 JSON: %v", out)
	}
	if out["http"] != float64(200) {
		t.Errorf("http = %v, want 200", out["http"])
	}
}

// TestCookieCloudTestUsesFormNotDB 页面没填 url/key/password 时不得用库里的凭证
func TestCookieCloudTestUsesFormNotDB(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newSetupInProgressServer(t, s, map[string]string{
		"secret.collector.douban.cookie_mode":          "cookiecloud",
		"secret.collector.douban.cookiecloud_url":      "https://cc.stored.example/get/xxxxxxxx",
		"secret.collector.douban.cookiecloud_key":      "stored-uuid",
		"secret.collector.douban.cookiecloud_password": "stored-pass",
	})

	form := url.Values{"cookie_mode": {"cookiecloud"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookiecloud/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("缺页面凭证应失败: %v", out)
	}
	sum, _ := out["summary"].(string)
	if !strings.Contains(sum, "失败") {
		t.Errorf("summary = %q", sum)
	}
}
