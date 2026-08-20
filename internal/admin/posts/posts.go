package posts

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/admin/onboard"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// postsFilter 全览筛选条当前值；With 改一维并清 page，供平铺枚举链接用
type postsFilter struct {
	Q, Status, Tag, AI, Handled, Source, Token string
	PageSize                                   int
}

func (f postsFilter) With(key, val string) template.URL {
	q := url.Values{}
	set := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			q.Set(k, v)
		}
	}
	set("q", f.Q)
	set("status", f.Status)
	set("tag", f.Tag)
	set("ai", f.AI)
	set("handled", f.Handled)
	set("source", f.Source)
	set("token", f.Token)
	if f.PageSize > 0 && f.PageSize != adminPostPageSize {
		q.Set("page_size", strconv.Itoa(f.PageSize))
	}
	if strings.TrimSpace(val) == "" {
		q.Del(key)
	} else {
		q.Set(key, val)
	}
	enc := q.Encode()
	if enc == "" {
		return template.URL("/admin/posts")
	}
	return template.URL("/admin/posts?" + enc)
}

// ToggleTag 点一下选中，再点取消；空了就等于点全部
func (f postsFilter) ToggleTag(text string) template.URL {
	return f.With("tag", ToggleFilterTags(f.Tag, text))
}

func ToggleFilterTags(csv, text string) string {
	text = strings.TrimSpace(text)
	cur := models.SplitFilterTags(csv)
	var next []string
	off := false
	for _, t := range cur {
		if t == text {
			off = true
			continue
		}
		next = append(next, t)
	}
	if !off && models.IsChipText(text) {
		next = append(next, text)
	}
	return strings.Join(next, ",")
}

const filterTagPreviewN = 10

func SplitFilterTagPreview(tags []models.FilterTag, n int) (top, more []models.FilterTag) {
	if n < 0 {
		n = 0
	}
	if len(tags) <= n {
		return tags, nil
	}
	return tags[:n], tags[n:]
}

func FilterTagsContain(tags []models.FilterTag, selected string) bool {
	want := map[string]bool{}
	for _, t := range models.SplitFilterTags(selected) {
		want[t] = true
	}
	if len(want) == 0 {
		return false
	}
	for _, t := range tags {
		if want[t.Text] {
			return true
		}
	}
	return false
}

// appendSelectedTags 当前多选里有选项列表没有的，补到末尾，免得选中态丢了
func appendSelectedTags(opts []models.FilterTag, selected string) []models.FilterTag {
	have := map[string]bool{}
	for _, o := range opts {
		have[o.Text] = true
	}
	for _, t := range models.SplitFilterTags(selected) {
		if have[t] {
			continue
		}
		opts = append(opts, models.FilterTag{Text: t})
		have[t] = true
	}
	return opts
}

// chipPosts 全览/API 列表只留地点、拉黑词、未命中、有用无用；AI 理由走独立列
func chipPosts(posts []models.RentPost) {
	for i := range posts {
		posts[i].Tags = models.ChipTags(posts[i].Tags, posts[i].AIReason)
	}
}

// postListFilterFromQuery 从 URL 取帖子列表筛选（admin / API 共用）
func postListFilterFromQuery(q url.Values) store.PostListFilter {
	src := ""
	if s, ok := models.ParseSource(q.Get("source")); ok {
		src = s.String()
	}
	return store.PostListFilter{
		Q:       q.Get("q"),
		Status:  q.Get("status"),
		Tag:     strings.Join(models.SplitFilterTags(q.Get("tag")), ","),
		Handled: q.Get("handled"),
		AI:      q.Get("ai"),
		Source:  src,
	}
}

// adminPostsQuery 拼 admin 帖子页 query（透传筛选 + token）
func adminPostsQuery(r *http.Request) url.Values {
	src := r.URL.Query()
	out := url.Values{}
	for _, k := range []string{"q", "status", "tag", "handled", "ai", "source", "page", "page_size", "token"} {
		if v := src.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

// adminPostsPath 拼 /admin/posts?... 重定向目标
func adminPostsPath(r *http.Request) string {
	q := adminPostsQuery(r)
	if len(q) == 0 {
		return "/admin/posts"
	}
	return "/admin/posts?" + q.Encode()
}

// handleHome 项目介绍页（GET /admin）
func (h *Handler) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.opts.Tmpl.ExecuteTemplate(w, "home", ports.MergePageCtx(ports.PageCtx(h.opts.RT, r, "home"), map[string]any{
		"Onboard": onboard.CollectOnboard(h.opts.RT.Get(), h.opts.RT.Secrets(), r.URL.Query().Get("token")),
	})); err != nil {
		pkglog.Component(pkglog.Admin).Error("介绍页渲染失败", "err", err)
	}
}

// handleAdmin 帖子列表页（GET /admin/posts）
// 页面数据 {Posts, Token, Q, Status, Tag, Handled}：Token/筛选透传 URL query，
// 供 nav 链接/筛选链接/表单 action 追加，保证鉴权开启时页面内跳转与提交不 401。
const adminPostPageSize = 20

func (h *Handler) handleAdmin(w http.ResponseWriter, r *http.Request) {
	f := postListFilterFromQuery(r.URL.Query())
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size <= 0 {
		size = adminPostPageSize
	}
	if size > 100 {
		size = 100
	}
	total, err := h.opts.DB.CountPosts(f)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("帖子计数失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	offset := (page - 1) * size
	posts, err := h.opts.DB.ListPosts(f, size, offset)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("帖子列表查询失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if err := h.opts.DB.AttachPostTags(posts); err != nil {
		pkglog.Component(pkglog.Admin).Warn("标签加载失败", "err", err)
	}
	if err := h.opts.DB.AttachAIReasons(posts); err != nil {
		pkglog.Component(pkglog.Admin).Warn("AI 原因加载失败", "err", err)
	}
	chipPosts(posts)
	tags, err := h.opts.DB.ListFilterTags()
	if err != nil {
		pkglog.Component(pkglog.Admin).Warn("标签聚合失败", "err", err)
		tags = nil
	}
	tags = appendSelectedTags(tags, f.Tag)
	tagsTop, tagsMore := SplitFilterTagPreview(tags, filterTagPreviewN)
	fq := adminPostsQuery(r).Encode()
	prevQ, nextQ := pageQuery(r, page-1), pageQuery(r, page+1)
	filter := postsFilter{
		Q: f.Q, Status: f.Status, Tag: f.Tag, AI: f.AI, Handled: f.Handled, Source: f.Source,
		Token: r.URL.Query().Get("token"), PageSize: size,
	}
	if err := h.opts.Tmpl.ExecuteTemplate(w, "admin", ports.MergePageCtx(ports.PageCtx(h.opts.RT, r, "posts"), map[string]any{
		"Posts":        posts,
		"Q":            f.Q,
		"Status":       f.Status,
		"Tag":          f.Tag,
		"Handled":      f.Handled,
		"AI":           f.AI,
		"Source":       f.Source,
		"Sources":      models.KnownSources(),
		"TagsTop":      tagsTop,
		"TagsMore":     tagsMore,
		"TagsMoreOpen": FilterTagsContain(tagsMore, f.Tag),
		"Filter":       filter,
		"Onboard":      onboard.CollectorOnboard(h.opts.RT.Get(), h.opts.RT.Secrets(), r.URL.Query().Get("token")),
		"Page":         page,
		"Pages":        pages,
		"Total":        total,
		"PageSize":     size,
		"HasPrev":      page > 1,
		"HasNext":      page < pages,
		"PrevQuery":    template.URL(prevQ),
		"NextQuery":    template.URL(nextQ),
		"FilterQuery":  template.URL(fq), // 避免 action 里 = 被 html/template 编成 %3d
		"Msg":          r.URL.Query().Get("msg"),
	})); err != nil {
		pkglog.Component(pkglog.Admin).Error("模板渲染失败", "err", err)
	}
}

func pageQuery(r *http.Request, page int) string {
	q := adminPostsQuery(r)
	if page <= 1 {
		q.Del("page")
	} else {
		q.Set("page", strconv.Itoa(page))
	}
	return q.Encode()
}

func (h *Handler) handlePostTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tags, err := h.opts.DB.ListFilterTags()
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("标签聚合失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	texts := make([]string, 0, len(tags))
	for _, t := range tags {
		texts = append(texts, t.Text)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"tags": texts, "items": tags})
}

// handleMark 标记反馈（POST /admin/mark，表单：post_id/action/reason）
// 仅接受 POST：GET 等请求一律 405，防止 <a>/<img> 链接触发写库。
// 表单值只读 PostForm（body），URL query 参数天然失效，杜绝 query 注入写库。
func (h *Handler) handleMark(w http.ResponseWriter, r *http.Request) {
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
	if err := h.opts.DB.AddUserFeedback(postID, action, r.PostFormValue("reason")); err != nil {
		pkglog.Component(pkglog.Admin).Error("反馈写入失败", "post_id", postID, "action", action, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("控制台标记反馈", "post_id", postID, "action", action)
	// PRG：防重复提交；透传筛选 + token，避免跳回后丢条件或 401
	http.Redirect(w, r, adminPostsPath(r), http.StatusSeeOther)
}

// handleHandled 独立「已处理」写/清 handled_at（POST /admin/handled）
// 表单：post_id + handled=1|0；只动 handled_at，不改 feedbacks，不写 posts.status。
func (h *Handler) handleHandled(w http.ResponseWriter, r *http.Request) {
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
		err = h.opts.DB.MarkPostHandled(postID)
	} else {
		err = h.opts.DB.ClearPostHandled(postID)
	}
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("已处理写入失败", "post_id", postID, "handled", handled, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("控制台标记已处理", "post_id", postID, "handled", handled)
	http.Redirect(w, r, adminPostsPath(r), http.StatusSeeOther)
}

const manualNotifyMax = 50

func redirectPostsMsg(w http.ResponseWriter, r *http.Request, msg string) {
	q := adminPostsQuery(r)
	if msg != "" {
		q.Set("msg", msg)
	}
	path := "/admin/posts"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// handleNotifySelected 勾选帖子后立刻发通知（POST /admin/notify，表单 post_id 可多值）
func (h *Handler) handleNotifySelected(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if h.opts.NotifyManual == nil {
		redirectPostsMsg(w, r, "通知未配置")
		return
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, raw := range r.PostForm["post_id"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) >= manualNotifyMax {
			break
		}
	}
	if len(ids) == 0 {
		redirectPostsMsg(w, r, "请先勾选帖子")
		return
	}
	group := "手动触发-" + time.Now().Format("010215:04:05")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := h.opts.NotifyManual.SendSelected(ctx, ids, group); err != nil {
		pkglog.Component(pkglog.Admin).Error("手动通知失败", "count", len(ids), "err", err)
		redirectPostsMsg(w, r, "发送失败："+err.Error())
		return
	}
	pkglog.Component(pkglog.Admin).Info("手动通知已发送", "count", len(ids), "group", group)
	redirectPostsMsg(w, r, "已发送 "+strconv.Itoa(len(ids))+" 条 · "+group)
}

// handlePosts 帖子列表（GET /api/posts?q=&status=&tag=&handled=&limit=&offset=，规格 7.1 + §6）
func (h *Handler) handlePosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	list, err := h.opts.DB.ListPosts(postListFilterFromQuery(q), limit, offset)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("帖子列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if err := h.opts.DB.AttachPostTags(list); err != nil {
		pkglog.Component(pkglog.Admin).Warn("标签加载失败", "err", err)
	}
	if err := h.opts.DB.AttachAIReasons(list); err != nil {
		pkglog.Component(pkglog.Admin).Warn("AI 原因加载失败", "err", err)
	}
	chipPosts(list)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"posts": list})
}

// handlePost 帖子详情（GET /api/posts/{id}）：post + filter_result + notifications + tags
func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/posts/")
	if idStr == "" {
		http.Error(w, "缺少帖子 id", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "帖子 id 无效", http.StatusBadRequest)
		return
	}
	post, ok, err := h.opts.DB.GetPost(id)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("查帖子详情失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "帖子不存在", http.StatusNotFound)
		return
	}
	filterResult, _, err := h.opts.DB.FilterResultByPostID(id)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("查筛选结果失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	notifications, err := h.opts.DB.ListNotificationsByPost(id)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("查通知失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	tags, err := h.opts.DB.ListTagsByPost(id)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("查标签失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	aiReason := ""
	if filterResult.AI != nil {
		aiReason = filterResult.AI.Reason
	}
	tags = models.ChipTags(tags, aiReason)
	post.Tags = tags
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"post":          post,
		"filter_result": filterResult,
		"notifications": notifications,
		"tags":          tags,
	})
}
