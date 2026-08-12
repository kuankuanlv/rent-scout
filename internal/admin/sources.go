package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// handleSources 源列表（GET /api/sources，规格 7.1）：name/enabled/cursor（store.GetCursor）。
// ctrl nil（采集未启动）→ 503；仅接受 GET，写操作走 handleSourceAction
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ctrl == nil {
		http.Error(w, "sources 不可用（采集未启动）", http.StatusServiceUnavailable)
		return
	}
	names := s.ctrl.Sources()
	type item struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Cursor  string `json:"cursor"`
	}
	items := make([]item, 0, len(names))
	for _, n := range names {
		cursor, _, _ := s.db.GetCursor(n)
		items = append(items, item{n, s.ctrl.SourceEnabled(n), cursor})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"sources": items})
}

// handleSourceAction 源动作（POST /api/sources/{name}/trigger | /enable | /disable，规格 7.1）。
// trigger → ctrl.Trigger；enable/disable → ctrl.SetEnabled；未知源 → 404；
// 仅接受 POST（GET 等一律 405，防止 <a>/<img> 链接触发写操作）
func (s *Server) handleSourceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ctrl == nil {
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
	var err error
	switch action {
	case "trigger":
		err = s.ctrl.Trigger(name)
	case "enable":
		err = s.ctrl.SetEnabled(name, true)
	case "disable":
		err = s.ctrl.SetEnabled(name, false)
	default:
		http.Error(w, "未知动作", http.StatusBadRequest)
		return
	}
	if err != nil {
		// 控制器仅对未知源返回错误 → 404
		slog.Warn("源动作失败", "source", name, "action", action, "err", err)
		http.Error(w, "源不存在", http.StatusNotFound)
		return
	}
	slog.Info("源动作", "source", name, "action", action)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}