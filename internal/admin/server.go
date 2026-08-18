package admin

import (
	"embed"
	"html/template"
	"net/http"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

func changedRestartKeys(before, updates map[string]string) []string {
	return ChangedRestartKeys(before, updates)
}

// Server 管理面 HTTP 服务
type Server struct {
	db             *store.Store
	rt             *config.HotConfig
	ctrl           SourceController
	tmpl           *template.Template
	onRulesChanged func()
	cookieProbe    CookieProbe
	llmProbe       LLMProbe
	notifyProbe    NotifyProbe
}

// NewServer 创建管理面服务
func NewServer(db *store.Store, rt *config.HotConfig, ctrl SourceController) *Server {
	t := template.New("").Funcs(template.FuncMap{
		"percent":        percent,
		"statusLabel":    statusLabel,
		"sourceLabel":    sourceLabel,
		"setupStepTitle": setupStepTitle,
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
	return &Server{db: db, rt: rt, ctrl: ctrl, tmpl: t}
}

// SetOnRulesChanged 规则能力变强时回调（新建/启用/加关键字等触发 replay；纯删除/禁用不触发）
func (s *Server) SetOnRulesChanged(fn func()) {
	s.onRulesChanged = fn
}

func (s *Server) SetCookieProbe(p CookieProbe) { s.cookieProbe = p }

func (s *Server) SetLLMProbe(p LLMProbe) { s.llmProbe = p }

func (s *Server) SetNotifyProbe(p NotifyProbe) { s.notifyProbe = p }

func (s *Server) notifyRulesChanged() {
	if s.onRulesChanged != nil {
		s.onRulesChanged()
	}
}

// Handler 路由装配
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/f", s.handleFeedback)
	mux.HandleFunc("/h", s.handleHandledLink)
	mux.HandleFunc("/api/post-tags", s.handlePostTags)
	mux.HandleFunc("/api/posts", s.handlePosts)
	mux.HandleFunc("/api/posts/", s.handlePost)
	mux.HandleFunc("/api/feedbacks", s.handleFeedbacks)
	mux.HandleFunc("/api/sources", s.handleSources)
	mux.HandleFunc("/api/sources/", s.handleSourceAction)
	mux.HandleFunc("/admin/setup", s.handleSetup)
	mux.HandleFunc("/admin/setup/import-defaults", s.handleImportDefaults)
	mux.HandleFunc("/admin/posts", s.handleAdmin)
	mux.HandleFunc("/admin", s.handleHome)
	mux.HandleFunc("/admin/mark", s.handleMark)
	mux.HandleFunc("/admin/handled", s.handleHandled)
	mux.HandleFunc("/admin/rules", s.handleRules)
	mux.HandleFunc("/admin/rules/", s.handleRulesID)
	mux.HandleFunc("/admin/config", s.handleConfig)
	mux.HandleFunc("/admin/config/save", s.handleConfig)
	mux.HandleFunc("/admin/config/export", s.handleConfigExport)
	mux.HandleFunc("/admin/config/history", s.handleConfigHistory)
	mux.HandleFunc("/admin/config/cookie/test", s.handleCookieTest)
	mux.HandleFunc("/admin/config/cookiecloud/test", s.handleCookieCloudTest)
	mux.HandleFunc("/admin/config/llm/test", s.handleLLMTest)
	mux.HandleFunc("/admin/config/llm/models", s.handleLLMModels)
	mux.HandleFunc("/admin/config/notify/test", s.handleNotifyTest)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/logs", s.handleLogs)
	mux.HandleFunc("/admin/logs/stream", s.handleLogsStream)
	mux.HandleFunc("/admin/logs/recent", s.handleLogsRecent)
	mux.HandleFunc("/admin/dead/reset", s.handleDeadReset)
	mux.HandleFunc("/admin/login", s.handleLogin)
	mux.HandleFunc("/admin/logout", s.handleLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	return s.auth(s.setupGate(mux))
}
