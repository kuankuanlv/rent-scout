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
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) DoubanPageResult {
		if c != "" {
			t.Errorf("匿名探测不应带 cookie, got len=%d", len(c))
		}
		return DoubanPageResult{OK: false, HTTP: 200, Snippet: "有异常请求从你的 IP 发出，请 登录 使用豆瓣"}
	}})

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
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) DoubanPageResult {
		return DoubanPageResult{OK: false, HTTP: 403, Snippet: "forbidden"}
	}})

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

func TestCookieTestWeiboProbesWeiboURL(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) DoubanPageResult {
		if !strings.Contains(rawURL, "weibo.com") {
			t.Errorf("微博探测 URL = %q", rawURL)
		}
		if c != "SUB=wb" {
			t.Errorf("cookie = %q", c)
		}
		return DoubanPageResult{OK: true, HTTP: 200}
	}})
	form := url.Values{
		"source":      {"weibo"},
		"cookie_mode": {"raw"},
		"cookie_raw":  {"SUB=wb"},
		"collector.weibo.tags": {"#test#"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("body=%s", rec.Body.String())
	}
}

func TestCookieTestRawEmptyDoesNotUseStored(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) DoubanPageResult {
		if c != "" {
			t.Errorf("空草稿不应回落到库里的 cookie, got %q", c)
		}
		return DoubanPageResult{OK: true, HTTP: 200}
	}})
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
	if out["ok"] != false {
		t.Errorf("页面未填 cookie 应失败: %v", out)
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
	if !strings.Contains(sum, "页面上的 CookieCloud") {
		t.Errorf("summary 应要求填写页面三元组: %q", sum)
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

func TestCookieCloudTestUsesPageTripleAndSource(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	probe := &recCloudProbe{}
	srv.SetCookieProbe(probe)
	if err := store.SetConfigBatch(s, map[string]string{
		"secret.collector.douban.cookiecloud_url":      "https://stored-douban.example",
		"secret.collector.douban.cookiecloud_key":      "db-uuid",
		"secret.collector.douban.cookiecloud_password": "db-pass",
		"secret.collector.weibo.cookiecloud_url":       "https://stored-weibo.example",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"source":               {"weibo"},
		"cookie_mode":          {"cookiecloud"},
		"cookiecloud_url":      {"https://page-weibo.example"},
		"cookiecloud_key":      {"page-uuid"},
		"cookiecloud_password": {"page-pass"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookiecloud/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if probe.source != "weibo" {
		t.Errorf("source = %q, want weibo", probe.source)
	}
	if probe.url != "https://page-weibo.example" {
		t.Errorf("url = %q, 应用页面草稿而不是库里的豆瓣/微博", probe.url)
	}
}

type recCloudProbe struct {
	source string
	url    string
}

func (p *recCloudProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (CookieCloudInspect, error) {
	p.source = source
	p.url = draft.CookiecloudURL
	return CookieCloudInspect{Cookie: "SUB=1", HTTPStatus: 200}, nil
}

func (p *recCloudProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) DoubanPageResult {
	return DoubanPageResult{OK: true, HTTP: 200}
}

type stubPageProbe struct {
	fn func(ctx context.Context, probeURL, rawCookie string) DoubanPageResult
}

func (s stubPageProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (CookieCloudInspect, error) {
	return testCookieProbe{}.InspectCookieCloud(ctx, draft, source)
}

func (s stubPageProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) DoubanPageResult {
	return s.fn(ctx, probeURL, rawCookie)
}
