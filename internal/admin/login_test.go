package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

func TestAuthLoginFlow(t *testing.T) {
	app := &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true, Token: "secret"}}
	srv := newTestServer(t, app, "secret", nil)
	var form url.Values

	// 1. 无凭证访问 /admin -> 302
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("expected redirect to login, got %d, loc=%s", rec.Code, rec.Header().Get("Location"))
	}

	// 2. 无凭证访问 /admin/feedbacks 表单 POST -> 302 登录页（保留现有页面 POST 语义，不回归）
	form = url.Values{"post_id": {"0"}, "action": {""}}
	req = httptest.NewRequest(http.MethodPost, "/admin/feedbacks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("expected redirect to login for form POST, got %d", rec.Code)
	}

	// 3. 无凭证访问 /api/posts -> 401 JSON
	req = httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Error("expected json content type")
	}

	// 3. GET /admin/login -> 200
	req = httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Error("body missing token input")
	}

	// 4. POST 正确 token -> 302 + Set-Cookie
	form = url.Values{"token": {"secret"}, "next": {"/admin"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin" {
		t.Errorf("expected redirect to /admin, got %d, loc=%s", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "rent_scout_token=secret") {
		t.Error("Set-Cookie missing rent_scout_token")
	}

	// 5. POST 错误 token -> 302 到 /admin/login 且 query 含 error
	form = url.Values{"token": {"wrong"}, "next": {"/admin"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/admin/login?error=") {
		t.Errorf("expected redirect to login error, got %d, loc=%s", rec.Code, rec.Header().Get("Location"))
	}

	// 6. POST 空 token -> 302 登录页
	form = url.Values{"token": {""}, "next": {"/admin"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("expected redirect to login, got %d", rec.Code)
	}

	// 7. 带有效 cookie 访问 /admin -> 200
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "rent_scout_token", Value: "secret"})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with cookie, got %d", rec.Code)
	}

	// 8. 带无效 cookie 访问 /admin -> 302
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "rent_scout_token", Value: "wrong"})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("expected redirect with wrong cookie, got %d", rec.Code)
	}

	// 9. ?token= query 兼容不回归 -> 200
	req = httptest.NewRequest(http.MethodGet, "/admin?token=secret", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with query token, got %d", rec.Code)
	}

	// 10. open redirect 防护 -> 302 到 /admin
	form = url.Values{"token": {"secret"}, "next": {"https://evil.com"}}
	req = httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Header().Get("Location") != "/admin" {
		t.Errorf("open redirect detected! loc=%s", rec.Header().Get("Location"))
	}

	// 11. /admin/logout -> 302 + 清除 cookie
	req = httptest.NewRequest(http.MethodGet, "/admin/logout", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("expected redirect, got %d", rec.Code)
	}
	// Go 的 http.SetCookie 将 MaxAge<0 序列化为 "Max-Age=0"（RFC 语义：使 cookie 立即过期）
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "rent_scout_token=") ||
		!strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want 清除 cookie（Max-Age=0）", rec.Header().Get("Set-Cookie"))
	}
}

func TestAuthDisabled(t *testing.T) {
	app := &config.AppConfig{Admin: config.AdminConfig{AuthRequired: false}}
	srv := newTestServer(t, app, "", nil)

	// 12. 鉴权关闭 -> 200
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 when auth disabled, got %d", rec.Code)
	}
}
