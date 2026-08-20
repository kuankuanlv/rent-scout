package admin

import (
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	cfgpage "rent-scout/internal/admin/config"
	"rent-scout/internal/admin/logs"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/admin/posts"
	"rent-scout/internal/admin/rules"
	"rent-scout/internal/admin/setup"
	"rent-scout/internal/admin/sources"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

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

// handleHealthz 健康检查（无鉴权）
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleMetrics 基础指标（规格 7.4：Prometheus 文本格式，无鉴权——监控抓取）
// 数据来自 store.TodayStats / ChannelStats（任务 2）
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	stats, err := s.db.TodayStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("今日统计失败: %v", err), http.StatusInternalServerError)
		return
	}
	channels, err := s.db.ChannelStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("渠道统计失败: %v", err), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "# HELP rent_scout_posts_collected_total 今日采集帖子总数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_collected_total gauge\n")
	fmt.Fprintf(w, "rent_scout_posts_collected_total %d\n", stats.Collected)
	fmt.Fprintf(w, "# HELP rent_scout_posts_passed_total 今日筛选通过帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_passed_total gauge\n")
	fmt.Fprintf(w, "rent_scout_posts_passed_total %d\n", stats.Passed)
	fmt.Fprintf(w, "# HELP rent_scout_posts_rejected_total 今日筛选拒绝帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_rejected_total gauge\n")
	fmt.Fprintf(w, "rent_scout_posts_rejected_total %d\n", stats.Rejected)
	fmt.Fprintf(w, "# HELP rent_scout_posts_pending_total 今日待判定帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_pending_total gauge\n")
	fmt.Fprintf(w, "rent_scout_posts_pending_total %d\n", stats.Pending)

	fmt.Fprintf(w, "# HELP rent_scout_notify_sent_total 渠道发送总数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_sent_total counter\n")
	fmt.Fprintf(w, "# HELP rent_scout_notify_failed_total 渠道发送失败数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_failed_total counter\n")
	fmt.Fprintf(w, "# HELP rent_scout_notify_dead_total 渠道死信数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_dead_total counter\n")
	for _, c := range channels {
		fmt.Fprintf(w, "rent_scout_notify_sent_total{channel=%q} %d\n", c.Channel, c.Sent)
		fmt.Fprintf(w, "rent_scout_notify_failed_total{channel=%q} %d\n", c.Channel, c.Failed)
		fmt.Fprintf(w, "rent_scout_notify_dead_total{channel=%q} %d\n", c.Channel, c.Dead)
	}
}

// statusLabel 帖子主状态码 → 全览页中文（与 collected|pending|passed|rejected 一一对应）
func statusLabel(s string) string {
	switch s {
	case "collected":
		return "已采集"
	case "passed":
		return "通过"
	case "rejected":
		return "拒绝"
	default:
		return s
	}
}

func sourceLabel(s string) string {
	switch s {
	case models.SourceDouban.String():
		return "豆瓣"
	case models.SourceWeibo.String():
		return "微博"
	default:
		return s
	}
}

// csvHas 逗号列表是否含某项（模板多选勾选态）
func csvHas(csv, item string) bool {
	return ports.CSVHas(csv, item)
}

// percent 百分比（保留 1 位小数）；b=0 返回 0
func percent(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return math.Round(float64(a)/float64(b)*1000) / 10
}
