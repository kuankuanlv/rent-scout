package admin

import (
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// pageCtx 页面公共数据：Token 透传 + Active 导航高亮
func pageCtx(r *http.Request, active string) map[string]any {
	return map[string]any{"Token": r.URL.Query().Get("token"), "Active": active}
}

// postListFilterFromQuery 从 URL 取帖子列表筛选（admin / API 共用）
func postListFilterFromQuery(q url.Values) store.PostListFilter {
	return store.PostListFilter{
		Q:       q.Get("q"),
		Status:  q.Get("status"),
		Tag:     q.Get("tag"),
		Handled: q.Get("handled"),
	}
}

// adminPostsQuery 拼 admin 帖子页 query（透传筛选 + token）
func adminPostsQuery(r *http.Request) url.Values {
	src := r.URL.Query()
	out := url.Values{}
	for _, k := range []string{"q", "status", "tag", "handled", "token"} {
		if v := src.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

// adminPostsPath 拼 /admin?... 重定向目标
func adminPostsPath(r *http.Request) string {
	q := adminPostsQuery(r)
	if len(q) == 0 {
		return "/admin"
	}
	return "/admin?" + q.Encode()
}

// 页面数据 {Posts, Token, Q, Status, Tag, Handled}：Token/筛选透传 URL query，
// 供 nav 链接/筛选链接/表单 action 追加，保证鉴权开启时页面内跳转与提交不 401。
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	f := postListFilterFromQuery(r.URL.Query())
	posts, err := s.db.ListPosts(f, 200, 0)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("[posts_list_failed] 帖子列表查询失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	fq := adminPostsQuery(r).Encode()
	if err := s.tmpl.ExecuteTemplate(w, "admin", mergePageCtx(pageCtx(r, "posts"), map[string]any{
		"Posts":       posts,
		"Q":           f.Q,
		"Status":      f.Status,
		"Tag":         f.Tag,
		"Handled":     f.Handled,
		"FilterQuery": template.URL(fq), // 避免 action 里 = 被 html/template 编成 %3d
	})); err != nil {
		pkglog.Component(pkglog.Admin).Error("[template_render_failed] 模板渲染失败", "err", err)
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
		pkglog.Component(pkglog.Admin).Error("[feedback_write_failed] 反馈写入失败", "post_id", postID, "action", action, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("[admin_marked] 控制台标记反馈", "post_id", postID, "action", action)
	// PRG：防重复提交；透传筛选 + token，避免跳回后丢条件或 401
	http.Redirect(w, r, adminPostsPath(r), http.StatusSeeOther)
}

// handleHandled 独立「已处理」写/清 handled_at（POST /admin/handled）
// 表单：post_id + handled=1|0；只动 handled_at，不改 feedbacks，不写 posts.status。
func (s *Server) handleHandled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	postID, _ := strconv.ParseInt(r.PostFormValue("post_id"), 10, 64)
	handled := r.PostFormValue("handled")
	if postID <= 0 || (handled != "0" && handled != "1") {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	var err error
	if handled == "1" {
		err = s.db.MarkPostHandled(postID)
	} else {
		err = s.db.ClearPostHandled(postID)
	}
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("[handled_write_failed] 已处理写入失败", "post_id", postID, "handled", handled, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("[admin_handled] 控制台标记已处理", "post_id", postID, "handled", handled)
	http.Redirect(w, r, adminPostsPath(r), http.StatusSeeOther)
}

// mergePageCtx 合并页面数据（pageCtx 优先保留 Token/Active）
func mergePageCtx(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}
