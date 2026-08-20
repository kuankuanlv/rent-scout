package sources

import (
	"encoding/json"
	"net/http"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
	"strings"
)

// handleSources 源列表（GET /api/sources，规格 7.1）：name/enabled/cursor（store.GetCursor）。
// ctrl nil（采集未启动）→ 503；仅接受 GET，写操作走 HandleSourceAction
func (h *Handler) handleSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.opts.Ctrl == nil {
		http.Error(w, "sources 不可用（采集未启动）", http.StatusServiceUnavailable)
		return
	}
	names := h.opts.Ctrl.Sources()
	type item struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Cursor  string `json:"cursor"`
	}
	items := make([]item, 0, len(names))
	for _, n := range names {
		raw, _, _ := h.opts.DB.GetCursor(n)
		p := store.ParseSourceProgress(raw)
		items = append(items, item{n, h.opts.Ctrl.SourceEnabled(n), p.Page})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"sources": items})
}

// handleSourceAction 源动作（POST /api/sources/{name}/enable | /disable | /reset）。
// enable/disable → ctrl.SetEnabled；reset 清进度；未知源 → 404；
// 仅接受 POST（GET 等一律 405，防止 <a>/<img> 链接触发写操作）
func (h *Handler) handleSourceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.opts.Ctrl == nil {
		http.Error(w, "sources 不可用（采集未启动）", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/sources/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	name, action := parts[0], parts[1]
	if action == "reset" {
		if !sourceKnown(h.opts.Ctrl, name) {
			http.Error(w, "源不存在", http.StatusNotFound)
			return
		}
		if err := h.opts.DB.ClearProgress(name); err != nil {
			pkglog.Component(pkglog.Admin).Warn("重置源进度失败", "source", name, "err", err)
			http.Error(w, "重置失败", http.StatusInternalServerError)
			return
		}
		pkglog.Component(pkglog.Admin).Info("源操作", "source", name, "action", "reset")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	var err error
	switch action {
	case "enable":
		err = h.opts.Ctrl.SetEnabled(name, true)
	case "disable":
		err = h.opts.Ctrl.SetEnabled(name, false)
	default:
		http.Error(w, "未知动作", http.StatusBadRequest)
		return
	}
	if err != nil {
		// 控制器仅对未知源返回错误 → 404
		pkglog.Component(pkglog.Admin).Warn("源操作失败", "source", name, "action", action, "err", err)
		http.Error(w, "源不存在", http.StatusNotFound)
		return
	}
	pkglog.Component(pkglog.Admin).Info("源操作", "source", name, "action", action)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func sourceKnown(ctrl ports.SourceController, name string) bool {
	for _, n := range ctrl.Sources() {
		if n == name {
			return true
		}
	}
	return false
}

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
