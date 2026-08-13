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

func TestCookieTestNoneSkipped(t *testing.T) {
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
	if out["ok"] != true {
		t.Errorf("ok = %v", out["ok"])
	}
	parse, _ := out["parse"].(map[string]any)
	online, _ := out["online"].(map[string]any)
	if parse["status"] != "skipped" {
		t.Errorf("parse.status = %v", parse["status"])
	}
	if online["status"] != "skipped" {
		t.Errorf("online.status = %v", online["status"])
	}
}

func TestCookieTestRawDraftNoWrite(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	before, _ := store.GetConfigMap(s)
	form := url.Values{
		"cookie_mode": {"raw"},
		"cookie_raw":  {"nosign"},
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
		t.Errorf("无等号草稿应 parse 失败, ok=%v", out["ok"])
	}
	parse, _ := out["parse"].(map[string]any)
	if parse["ok"] != false {
		t.Errorf("parse.ok = %v", parse["ok"])
	}
	after, _ := store.GetConfigMap(s)
	if after["secret.collector.douban.cookie_raw"] != before["secret.collector.douban.cookie_raw"] {
		t.Error("test 不应写库 cookie_raw")
	}
}

func TestCookieTestRawUsesStoredWhenEmpty(t *testing.T) {
	orig := cookie.OnlineProbe
	cookie.OnlineProbe = func(ctx context.Context, c string, client *http.Client) (bool, string, string) {
		if c != "dbcl2=storedvalue123456" {
			t.Errorf("应使用已存 cookie, got len=%d", len(c))
		}
		return true, "ok", "mocked"
	}
	defer func() { cookie.OnlineProbe = orig }()

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
	parse, _ := out["parse"].(map[string]any)
	if parse["ok"] != true {
		t.Errorf("应沿用已存 raw: parse=%v", parse)
	}
	if out["ok"] != true {
		t.Errorf("mocked online 应 ok: %v", out)
	}
	masked, _ := parse["preview_masked"].(string)
	if strings.Contains(masked, "storedvalue123456") {
		t.Errorf("脱敏后不应含全文: %q", masked)
	}
	body := rec.Body.String()
	if strings.Contains(body, "storedvalue123456") {
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
	parse, _ := out["parse"].(map[string]any)
	detail, _ := parse["detail"].(string)
	if !strings.Contains(detail, "file") {
		t.Errorf("应提示 file 已移除: %v", parse)
	}
}
