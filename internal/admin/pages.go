package admin

import (
	"embed"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"rent-scout/internal/models"
)

//go:embed templates/*.html
var templatesFS embed.FS

// handleAdmin 帖子全览页（GET /admin?status=）
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	posts, err := s.db.ListPosts(r.URL.Query().Get("status"), 200, 0)
	if err != nil {
		slog.Error("帖子列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "admin", map[string]any{"Posts": posts}); err != nil {
		slog.Error("模板渲染失败", "err", err)
	}
}

// handleMark 标记反馈（POST /admin/mark，表单：post_id/action/reason）
func (s *Server) handleMark(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	postID, _ := strconv.ParseInt(r.FormValue("post_id"), 10, 64)
	action := r.FormValue("action")
	if postID <= 0 || (action != models.FeedbackUseful && action != models.FeedbackUseless) {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if err := s.db.InsertFeedback(models.Feedback{PostID: postID, Action: action, Reason: r.FormValue("reason"), CreatedAt: time.Now()}); err != nil {
		slog.Error("写反馈失败", "post_id", postID, "action", action, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	slog.Info("admin_marked", "post_id", postID, "action", action)
	http.Redirect(w, r, "/admin", http.StatusSeeOther) // PRG：防重复提交
}