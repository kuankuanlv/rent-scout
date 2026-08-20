package logs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/pkglog"
)

// handleLogs 系统日志页（GET /admin/logs）：SSE 滚动，成熟方案是内存 ring + EventSource
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.opts.Tmpl.ExecuteTemplate(w, "logs", ports.MergePageCtx(ports.PageCtx(h.opts.RT, r, "logs"), map[string]any{
		"MemoryLines": pkglog.HubCap(),
	})); err != nil {
		pkglog.Component(pkglog.Admin).Error("模板渲染失败", "err", err)
	}
}

// handleLogsRecent 最近日志 JSON（GET /admin/logs/recent?n=200）
func (h *Handler) handleLogsRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	capN := pkglog.HubCap()
	if n <= 0 {
		n = capN
	}
	if n > capN {
		n = capN
	}
	ports.WriteJSON(w, map[string]any{"logs": pkglog.RecentLogs(n), "cap": capN})
}

// handleLogsStream SSE 实时尾巴（GET /admin/logs/stream）
func (h *Handler) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "需要支持 flush 的连接", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := pkglog.SubscribeLogs()
	defer cancel()

	var lastSeq uint64
	for _, line := range pkglog.RecentLogs(0) {
		if err := writeSSE(w, line); err != nil {
			return
		}
		lastSeq = line.Seq
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case line, ok := <-ch:
			if !ok {
				return
			}
			if line.Seq <= lastSeq {
				continue
			}
			if err := writeSSE(w, line); err != nil {
				return
			}
			lastSeq = line.Seq
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, line pkglog.Line) error {
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}
