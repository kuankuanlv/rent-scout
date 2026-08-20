package rules

import (
	"html/template"

	"net/http"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// Options 规则管理 handler 依赖
type Options struct {
	DB             *store.Store
	RT             *config.HotConfig
	Tmpl           *template.Template
	OnRulesChanged func()
}

// Handler 规则管理（/admin/rules）处理器
type Handler struct {
	opts Options
}

// New 创建规则管理 handler
func New(opts Options) *Handler {
	return &Handler{opts: opts}
}

// notifyRulesChanged 规则变更回调（根包注入，用于触发采集器重载）
func (h *Handler) notifyRulesChanged() {
	if h.opts.OnRulesChanged != nil {
		h.opts.OnRulesChanged()
	}
}

// Routes 注册规则管理路由
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/rules", h.handleRules)
	mux.HandleFunc("/admin/rules/", h.handleRulesID)
}

// SetOnRulesChanged 注入规则变更回调（根包装配用）
func (h *Handler) SetOnRulesChanged(fn func()) { h.opts.OnRulesChanged = fn }
