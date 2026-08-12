package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// handleFeedbacks 反馈写入接口（POST /api/feedbacks，规格 7.1）。
// 鉴权：带 query sig → HMAC 校验（卡片链接场景）；否则走管理 token（auth 中间件）
func (s *Server) handleFeedbacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		PostID  int64  `json:"post_id"`
		Channel string `json:"channel"`
		Action  string `json:"action"` // useful / useless
		Reason  string `json:"reason"` // 可选
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	if s.rt.Get().Admin.AuthRequired && q.Get("sig") == "" {
		// 无签名：由 auth 中间件已校验管理 token；此处只需通过
	} else if err := s.verifyFeedbackSig(in.PostID, in.Action, q.Get("exp"), q.Get("sig")); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if in.PostID <= 0 || (in.Action != "useful" && in.Action != "useless") {
		http.Error(w, "post_id/action 无效", http.StatusBadRequest)
		return
	}
	if err := s.db.InsertFeedback(models.Feedback{PostID: in.PostID, Channel: in.Channel, Action: in.Action, Reason: in.Reason, CreatedAt: time.Now()}); err != nil {
		slog.Error("写反馈失败", "post_id", in.PostID, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handlePosts 帖子列表（GET /api/posts?status=&limit=&offset=，规格 7.1）
func (s *Server) handlePosts(w http.ResponseWriter, r *http.Request) {
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
	posts, err := s.db.ListPosts(q.Get("status"), limit, offset)
	if err != nil {
		slog.Error("帖子列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"posts": posts})
}

// handlePost 帖子详情（GET /api/posts/{id}）：post + filter_result + notifications + feedbacks
func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
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
	post, ok, err := s.db.GetPost(id)
	if err != nil {
		slog.Error("查帖子详情失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "帖子不存在", http.StatusNotFound)
		return
	}
	filterResult, _, err := s.db.FilterResultByPostID(id)
	if err != nil {
		slog.Error("查筛选结果失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	notifications, err := s.db.ListNotificationsByPost(id)
	if err != nil {
		slog.Error("查通知失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	feedbacks, err := s.db.ListFeedbacksByPost(id)
	if err != nil {
		slog.Error("查反馈失败", "id", id, "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"post":          post,
		"filter_result": filterResult,
		"notifications": notifications,
		"feedbacks":     feedbacks,
	})
}