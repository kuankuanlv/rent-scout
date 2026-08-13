package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestCookieTestNoneProbesOnline(t *testing.T) {
	orig := cookie.ProbePage
	cookie.ProbePage = func(ctx context.Context, rawURL, c string, client *http.Client) cookie.DoubanPageResult {
		if c != "" {
			t.Errorf("匿名探测不应带 cookie, got len=%d", len(c))
		}
		return cookie.DoubanPageResult{OK: false, HTTP: 200, Snippet: "有异常请求从你的 IP 发出，请 登录 使用豆瓣"}
	}
	defer func() { cookie.ProbePage = orig }()

	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{"cookie_mode": {"none"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
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
		t.Errorf("无 cookie 风控应失败, ok=%v", out["ok"])
	}
	if out["http"] != float64(200) {
		t.Errorf("http = %v, want 200", out["http"])
	}
	snip, _ := out["snippet"].(string)
	if !strings.Contains(snip, "请 登录") && !strings.Contains(snip, "异常请求") {
		t.Errorf("snippet 应含豆瓣原文: %q", snip)
	}
}

func TestCookieTestRawDraftNoWrite(t *testing.T) {
	orig := cookie.ProbePage
	cookie.ProbePage = func(ctx context.Context, rawURL, c string, client *http.Client) cookie.DoubanPageResult {
		return cookie.DoubanPageResult{OK: false, HTTP: 403, Snippet: "forbidden"}
	}
	defer func() { cookie.ProbePage = orig }()

	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	before, _ := store.GetConfigMap(s)
	form := url.Values{
		"cookie_mode": {"raw"},
		"cookie_raw":  {"bid=abc"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
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
		t.Errorf("探测失败应 ok=false, got %v", out["ok"])
	}
	if out["http"] != float64(403) {
		t.Errorf("http = %v, want 403", out["http"])
	}
	after, _ := store.GetConfigMap(s)
	if after["secret.collector.douban.cookie_raw"] != before["secret.collector.douban.cookie_raw"] {
		t.Error("test 不应写库 cookie_raw")
	}
}

func TestCookieTestRawUsesStoredWhenEmpty(t *testing.T) {
	orig := cookie.ProbePage
	cookie.ProbePage = func(ctx context.Context, rawURL, c string, client *http.Client) cookie.DoubanPageResult {
		if c != "dbcl2=storedvalue123456" {
			t.Errorf("应使用已存 cookie, got %q", c)
		}
		return cookie.DoubanPageResult{OK: true, HTTP: 200}
	}
	defer func() { cookie.ProbePage = orig }()

	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	if err := store.SetConfigBatch(s, map[string]string{
		"secret.collector.douban.cookie_mode": "raw",
		"secret.collector.douban.cookie_raw":  "dbcl2=storedvalue123456",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"cookie_mode": {"raw"}, "cookie_raw": {""}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
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
	if out["ok"] != true {
		t.Errorf("mocked page 应 ok: %v", out)
	}
	if strings.Contains(rec.Body.String(), "storedvalue123456") {
		t.Error("响应 JSON 不应含明文 cookie")
	}
}

func TestCookieTestMethodNotAllowed(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/config/cookie/test", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestCookieTestFileDraftRejected(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{"cookie_mode": {"file"}, "cookie_file": {"/tmp/c.txt"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
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
		t.Errorf("file 草稿应失败: %v", out)
	}
	snip, _ := out["snippet"].(string)
	if !strings.Contains(snip, "file") {
		t.Errorf("应提示 file 已移除: %v", out)
	}
}

func TestCookieCloudTestIncomplete(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

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
		t.Errorf("缺 url/key 应失败: %v", out)
	}
	sum, _ := out["summary"].(string)
	if !strings.Contains(sum, "不完整") && !strings.Contains(sum, "url") {
		t.Errorf("summary 应说明配置不完整: %q", sum)
	}
}

func TestCookieCloudTestOK(t *testing.T) {
	cc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cookie_data":{"www.douban.com":[{"name":"bid","value":"abc123","domain":".douban.com"}]}}`))
	}))
	defer cc.Close()

	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{
		"cookie_mode":          {"cookiecloud"},
		"cookiecloud_url":      {cc.URL},
		"cookiecloud_key":      {"uuid-1"},
		"cookiecloud_password": {"pass"},
	}
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
	if out["ok"] != true {
		t.Fatalf("应通过: %v", out)
	}
	if out["summary"] != "通过" {
		t.Errorf("summary = %v", out["summary"])
	}
	names, _ := out["cookie_names"].([]any)
	if len(names) == 0 {
		t.Errorf("应列出 cookie 名: %v", out)
	}
}

func TestCookieHeaderPreviews(t *testing.T) {
	got := cookie.CookieHeaderPreviews("dbcl2=abcdefghijklmnop; bid=xyz")
	if len(got) != 2 {
		t.Fatalf("previews=%v", got)
	}
	if strings.Contains(got[0], "abcdefghijklmnop") {
		t.Errorf("不应含明文: %v", got)
	}
}
