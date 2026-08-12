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
}

// Server 管理面 HTTP 服务（规格 7.1）：路由装配 + 鉴权中间件
type Server struct {
	db    *store.Store
	rt    *config.Runtime
	token string             // main 解析后的有效 token（固定配置或随机生成；AuthRequired=false 时可为空）
	ctrl  SourceController   // 任务 9 注入；nil 时 /api/sources 返回 503
	tmpl  *template.Template // embed 模板（任务 6-8 装配，可 nil 直到页面任务）
}

// NewServer 创建管理面服务
func NewServer(db *store.Store, rt *config.Runtime, token string, ctrl SourceController) *Server {
	return &Server{db: db, rt: rt, token: token, ctrl: ctrl}
}

// Handler 路由装配（任务 4-9 逐步挂 handler；本任务先挂 healthz/metrics）
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/f", s.handleFeedback)
	mux.HandleFunc("/api/posts", s.handlePosts)
	mux.HandleFunc("/api/posts/", s.handlePost) // 前缀匹配，handler 内解析 id
	mux.HandleFunc("/api/feedbacks", s.handleFeedbacks)
	return s.auth(mux)
}
