package sources

import (
	"net/http"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/store"
)

// Options 采集源管理 handler 依赖
type Options struct {
	Ctrl ports.SourceController
	DB   *store.Store
}

// Handler 采集源管理（/admin/sources、/api/sources*）处理器
type Handler struct {
	opts Options
}

// New 创建采集源管理 handler
func New(opts Options) *Handler {
	return &Handler{opts: opts}
}

// Routes 注册采集源管理路由
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sources", h.handleSources)
	mux.HandleFunc("/api/sources/", h.handleSourceAction)
}
