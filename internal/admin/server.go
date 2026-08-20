package admin

import (
	"embed"
	"html/template"
	"net/http"

	cfgpage "rent-scout/internal/admin/config"
	"rent-scout/internal/admin/logs"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/admin/posts"
	"rent-scout/internal/admin/rules"
	"rent-scout/internal/admin/setup"
	"rent-scout/internal/admin/sources"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

func changedRestartKeys(before, updates map[string]string) []string {
	return cfgpage.ChangedRestartKeys(before, updates)
}

// Server 管理面 HTTP 服务
type Server struct {
	db             *store.Store
	rt             *config.HotConfig
	ctrl           ports.SourceController
	tmpl           *template.Template
	onRulesChanged func()
	cookieProbe    ports.CookieProbe
	llmProbe       ports.LLMProbe
	notifyProbe    ports.NotifyProbe
	notifyManual   ports.NotifyManual

	posts   *posts.Handler
	config  *cfgpage.Handler
	rules   *rules.Handler
	setup   *setup.Handler
	sources *sources.Handler
	logs    *logs.Handler
}

// NewServer 创建管理面服务
func NewServer(db *store.Store, rt *config.HotConfig, ctrl ports.SourceController) *Server {
	t := template.New("").Funcs(template.FuncMap{
		"percent":        percent,
		"statusLabel":    statusLabel,
		"sourceLabel":    sourceLabel,
		"setupStepTitle": setup.SetupStepTitle,
		"csvHas":         csvHas,
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
	return &Server{
		db:      db,
		rt:      rt,
		ctrl:    ctrl,
		tmpl:    t,
		posts:   posts.New(posts.Options{DB: db, RT: rt, Tmpl: t}),
		config:  cfgpage.New(cfgpage.Options{DB: db, RT: rt, Tmpl: t, Ctrl: ctrl}),
		rules:   rules.New(rules.Options{DB: db, RT: rt, Tmpl: t}),
		setup:   setup.New(setup.Options{DB: db, RT: rt, Tmpl: t}),
		sources: sources.New(sources.Options{Ctrl: ctrl, DB: db}),
		logs:    logs.New(logs.Options{RT: rt, Tmpl: t}),
	}
}

// SetOnRulesChanged 规则能力变强时回调（新建/启用/加关键字等触发 replay；纯删除/禁用不触发）
func (s *Server) SetOnRulesChanged(fn func()) {
	s.onRulesChanged = fn
	s.rules.SetOnRulesChanged(fn)
}

func (s *Server) SetCookieProbe(p ports.CookieProbe) {
	s.cookieProbe = p
	s.config.SetCookieProbe(p)
}

func (s *Server) SetLLMProbe(p ports.LLMProbe) {
	s.llmProbe = p
	s.config.SetLLMProbe(p)
}

func (s *Server) SetNotifyProbe(p ports.NotifyProbe) {
	s.notifyProbe = p
	s.config.SetNotifyProbe(p)
}

func (s *Server) SetNotifyManual(p ports.NotifyManual) {
	s.notifyManual = p
	s.posts.SetNotifyManual(p)
}

// Handler 路由装配
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	s.posts.Routes(mux)
	s.config.Routes(mux)
	s.rules.Routes(mux)
	s.setup.Routes(mux)
	s.sources.Routes(mux)
	s.logs.Routes(mux)
	mux.HandleFunc("/admin/login", s.handleLogin)
	mux.HandleFunc("/admin/logout", s.handleLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	return s.auth(s.setup.Gate(mux))
}
