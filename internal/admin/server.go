package admin

import (
	"html/template"
	"net/http"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// SourceController 采集源控制接口（main 注入 collector.Runner；admin 不依赖 collector 包）
type SourceController interface {
	SetEnabled(name string, on bool) error
	Trigger(name string) error
	Sources() []string
	SourceEnabled(name string) bool
}

// Server 管理面 HTTP 服务
type Server struct {
	db   *store.Store
	rt   *config.Runtime
	ctrl SourceController
	tmpl *template.Template
}

// NewServer 创建管理面服务
func NewServer(db *store.Store, rt *config.Runtime, ctrl SourceController) *Server {
	t := template.New("").Funcs(template.FuncMap{
		"percent":        percent,
		"setupStepTitle": setupStepTitle,
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"sub": func(a, b int) int { return a - b },
	})
	t = template.Must(t.ParseFS(templatesFS, "templates/*.html"))
	return &Server{db: db, rt: rt, ctrl: ctrl, tmpl: t}
}

// Handler 路由装配
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/f", s.handleFeedback)
	mux.HandleFunc("/h", s.handleHandledLink)
	mux.HandleFunc("/api/posts", s.handlePosts)
	mux.HandleFunc("/api/posts/", s.handlePost)
	mux.HandleFunc("/api/feedbacks", s.handleFeedbacks)
	mux.HandleFunc("/api/sources", s.handleSources)
	mux.HandleFunc("/api/sources/", s.handleSourceAction)
	mux.HandleFunc("/admin/setup", s.handleSetup)
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/admin/mark", s.handleMark)
	mux.HandleFunc("/admin/handled", s.handleHandled)
	mux.HandleFunc("/admin/rules", s.handleRules)
	mux.HandleFunc("/admin/rules/", s.handleRulesID)
	mux.HandleFunc("/admin/config", s.handleConfig)
	mux.HandleFunc("/admin/config/save", s.handleConfig)
	mux.HandleFunc("/admin/config/cookie/test", s.handleCookieTest)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/dead/reset", s.handleDeadReset)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	return s.auth(s.setupGate(mux))
}
