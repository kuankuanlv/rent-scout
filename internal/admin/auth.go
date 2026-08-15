package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"rent-scout/internal/store"
)

// auth 全局鉴权：管理台与 API 走 token；回调 /f /h 和探活 /healthz 放行。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicNoAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !store.IsSetupComplete(s.db) && setupExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.rt.Get().Admin.AuthRequired && !s.validToken(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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

func setupExempt(path string) bool {
	return path == "/admin/setup" || path == CookieTestPath || path == CookieCloudTestPath
}

// validToken 校验 token：URL ?token= 或 Bearer，与 HotConfig 中 admin.token 比较
func (s *Server) validToken(r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	want := s.rt.Get().Admin.Token
	return tok != "" && want != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}
