package admin

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		next := r.URL.Query().Get("next")
		if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/admin"
		}
		data := map[string]any{
			"Title": "访问验证",
			"Error": r.URL.Query().Get("error"),
			"Next":  next,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmpl.ExecuteTemplate(w, "login", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseForm()
	token := r.Form.Get("token")
	next := r.Form.Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/admin"
	}

	app := s.rt.Get()
	if app.Admin.AuthRequired {
		want := app.Admin.Token
		// 无论 want 是否为空，都要进行 constant-time 比较，但 token 必须非空且匹配
		if token == "" || want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
			http.Redirect(w, r, "/admin/login?error=令牌不正确&next="+url.QueryEscape(next), http.StatusFound)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "rent_scout_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
	})
	http.Redirect(w, r, next, http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "rent_scout_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}
