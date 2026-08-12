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

// handleAdmin 帖子全览页（GET /admin?status=&token=）
// 页面数据 {Posts, Token}：Token 透传 URL 上的 ?token=（鉴权开启时），供 nav 链接/筛选链接/表单 action 追加，
// 保证鉴权开启时页面内跳转与提交不 401。后续页面（rules/stats）复用 nav define 时，
// 其页面数据必须提供 Token 字段（可为空串），否则鉴权开启时链接失效。
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	posts, err := s.db.ListPosts(r.URL.Query().Get("status"), 200, 0)
	if err != nil {
		slog.Error("帖子列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "admin", map[string]any{"Posts": posts, "Token": r.URL.Query().Get("token")}); err != nil {
		slog.Error("模板渲染失败", "err", err)
	}
}

// handleMark 标记反馈（POST /admin/mark，表单：post_id/action/reason）
// 仅接受 POST：GET 等请求一律 405，防止 <a>/<img> 链接触发写库。
// 表单值只读 PostForm（body），URL query 参数天然失效，杜绝 query 注入写库。
func (s *Server) handleMark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	postID, _ := strconv.ParseInt(r.PostFormValue("post_id"), 10, 64)
	action := r.PostFormValue("action")
	if postID <= 0 || (action != models.FeedbackUseful && action != models.FeedbackUseless) {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if err := s.db.InsertFeedback(models.Feedback{PostID: postID, Action: action, Reason: r.PostFormValue("reason"), CreatedAt: time.Now()}); err != nil {
		slog.Error("写反馈失败", "post_id", postID, "action", action, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	slog.Info("admin_marked", "post_id", postID, "action", action)
	// PRG：防重复提交；鉴权开启时把 token 带回重定向目标，避免跳回 /admin 后 401
	redirectTo := "/admin"
	if tok := r.URL.Query().Get("token"); tok != "" {
		redirectTo += "?token=" + tok
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}
