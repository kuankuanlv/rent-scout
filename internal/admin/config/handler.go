package config

import (
	"html/template"

	"net/http"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Options 配置页面 handler 依赖
type Options struct {
	DB          *store.Store
	RT          *config.HotConfig
	Tmpl        *template.Template
	Ctrl        ports.SourceController
	CookieProbe ports.CookieProbe
	LLMProbe    ports.LLMProbe
	NotifyProbe ports.NotifyProbe
}

// Handler 配置页面（/admin/config*）处理器
type Handler struct {
	opts Options
}

// New 创建配置页面 handler
func New(opts Options) *Handler {
	return &Handler{opts: opts}
}

// Routes 注册配置页面路由
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/config", h.handleConfig)
	mux.HandleFunc("/admin/config/save", h.handleConfig)
	mux.HandleFunc("/admin/config/export", h.handleConfigExport)
	mux.HandleFunc("/admin/config/import", h.handleConfigImport)
	mux.HandleFunc("/admin/config/history", h.handleConfigHistory)
	mux.HandleFunc("/admin/config/cookie/test", h.handleCookieTest)
	mux.HandleFunc("/admin/config/cookiecloud/test", h.handleCookieCloudTest)
	mux.HandleFunc("/admin/config/llm/test", h.handleLLMTest)
	mux.HandleFunc("/admin/config/llm/models", h.handleLLMModels)
	mux.HandleFunc("/admin/config/notify/test", h.handleNotifyTest)
}

// SetCookieProbe 注入 Cookie 探测器（根包装配用）
func (h *Handler) SetCookieProbe(p ports.CookieProbe) { h.opts.CookieProbe = p }

// SetLLMProbe 注入 LLM 探测器（根包装配用）
func (h *Handler) SetLLMProbe(p ports.LLMProbe) { h.opts.LLMProbe = p }

// SetNotifyProbe 注入通知探测器（根包装配用）
func (h *Handler) SetNotifyProbe(p ports.NotifyProbe) { h.opts.NotifyProbe = p }
