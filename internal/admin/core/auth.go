package core

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"rent-scout/internal/admin/pages"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/store"
)

// auth 全局鉴权：管理台与 API 走 token；回调 /f /h 和探活 /healthz 放行。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicNoAuth(r.URL.Path) || loginExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !store.IsSetupComplete(s.db) && setupExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.rt.Get().Admin.AuthRequired && !s.validToken(r) {
			if ports.WantsJSON(r) || strings.HasPrefix(r.URL.Path, "/api/") {
				ports.WriteJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			// 对于管理页面，未授权时跳转到登录页
			// 只有对于非豁免的页面，才需要跳转
			http.Redirect(w, r, "/admin/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func publicNoAuth(path string) bool {
	switch path {
	case "/f", "/h", "/healthz":
		return true
	default:
		return false
	}
}

func loginExempt(path string) bool {
	return path == "/admin/login" || path == "/admin/logout"
}

func setupExempt(path string) bool {
	return path == "/admin/setup" || path == "/admin/setup/import-defaults" || path == pages.CookieTestPath || path == pages.CookieCloudTestPath
}

// validToken 校验 token：URL ?token=、Bearer 或 Cookie；与 HotConfig 中 admin.token 比较
func (s *Server) validToken(r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	if tok == "" {
		if c, err := r.Cookie("rent_scout_token"); err == nil {
			tok = c.Value
		}
	}
	want := s.rt.Get().Admin.Token
	return tok != "" && want != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}
