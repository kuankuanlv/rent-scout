package admin

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// auth 鉴权中间件：/healthz、/metrics 豁免；auth_required 实时读 rt（热加载 10s 生效）。
// 认证方式：Authorization: Bearer <token> 或 URL ?token=<token>（constant-time 比较）
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
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

// validToken 校验请求携带的 token：URL ?token= 或 Authorization: Bearer（后者优先），constant-time 比较
func (s *Server) validToken(r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1
}
