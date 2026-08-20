package posts

import (
	"html/template"

	"net/http"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Options 帖子页面 handler 依赖
type Options struct {
	DB           *store.Store
	RT           *config.HotConfig
	Tmpl         *template.Template
	NotifyManual ports.NotifyManual
}

// Handler 帖子页面（/admin、/admin/posts*、/admin/stats、反馈链接）处理器
type Handler struct {
	opts Options
}

// New 创建帖子页面 handler
func New(opts Options) *Handler {
	return &Handler{opts: opts}
}

// Routes 注册帖子页面路由
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/f", h.handleFeedback)
	mux.HandleFunc("/h", h.handleHandledLink)
	mux.HandleFunc("/api/post-tags", h.handlePostTags)
	mux.HandleFunc("/api/posts", h.handlePosts)
	mux.HandleFunc("/api/posts/", h.handlePost)
	mux.HandleFunc("/api/feedbacks", h.handleFeedbacks)
	mux.HandleFunc("/admin/posts", h.handleAdmin)
	mux.HandleFunc("/admin", h.handleHome)
	mux.HandleFunc("/admin/mark", h.handleMark)
	mux.HandleFunc("/admin/notify", h.handleNotifySelected)
	mux.HandleFunc("/admin/handled", h.handleHandled)
	mux.HandleFunc("/admin/stats", h.handleStats)
	mux.HandleFunc("/admin/dead/reset", h.handleDeadReset)
}

// SetNotifyManual 注入手动通知器（根包装配用）
func (h *Handler) SetNotifyManual(p ports.NotifyManual) { h.opts.NotifyManual = p }
