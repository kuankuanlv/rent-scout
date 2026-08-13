package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

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
