package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

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
