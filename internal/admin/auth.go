package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"rent-scout/internal/store"
)

// auth 鉴权中间件：setup 未完成时 /admin/setup 豁免；healthz/metrics/f/h 豁免
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" || path == "/metrics" || path == "/f" || path == "/h" {
			next.ServeHTTP(w, r)
			return
		}
		if !store.IsSetupComplete(s.db) && (path == "/admin/setup" || path == CookieTestPath) {
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

// validToken 校验 token：URL ?token= 或 Bearer，与 HotConfig 中 admin.token 比较
func (s *Server) validToken(r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	want := s.rt.Get().Admin.Token
	return tok != "" && want != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1
}
