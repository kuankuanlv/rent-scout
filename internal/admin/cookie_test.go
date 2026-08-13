package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rent-scout/internal/collector"
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
	orig := collector.OnlineProbe
	collector.OnlineProbe = func(ctx context.Context, cookie string, client *http.Client) (bool, string, string) {
		if cookie != "dbcl2=storedvalue123456" {
			t.Errorf("应使用已存 cookie, got len=%d", len(cookie))
		}
		return true, "ok", "mocked"
	}
	defer func() { collector.OnlineProbe = orig }()

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

func TestCookieTestFileDraft(t *testing.T) {
	orig := collector.OnlineProbe
	collector.OnlineProbe = func(ctx context.Context, cookie string, client *http.Client) (bool, string, string) {
		return true, "ok", "mocked"
	}
	defer func() { collector.OnlineProbe = orig }()

	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte("k=v; x=y"), 0o644); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"cookie_mode": {"file"}, "cookie_file": {path}}
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
		t.Errorf("file 草稿应 parse ok: %v", parse)
	}
}
