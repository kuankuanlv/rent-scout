package setup

import (
	"html/template"

	"net/http"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Options 引导安装 handler 依赖
type Options struct {
	DB   *store.Store
	RT   *config.HotConfig
	Tmpl *template.Template
}

// Handler 引导安装（/admin/setup*）处理器
type Handler struct {
	opts Options
}

// New 创建引导安装 handler
func New(opts Options) *Handler {
	return &Handler{opts: opts}
}

// Routes 注册引导安装路由
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/setup", h.handleSetup)
	mux.HandleFunc("/admin/setup/import-defaults", h.handleImportDefaults)
}
