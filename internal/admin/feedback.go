package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"rent-scout/internal/models"
)

// verifyFeedbackSig 校验反馈链接签名（规格 7.1）。
// token 空（鉴权关闭）= 链接不签名，直接放行；有签名则 constant-time 校验 + exp 过期检查
func (s *Server) verifyFeedbackSig(postID int64, action, exp, sig string) error {
	if s.token == "" {
		return nil // 全开放场景（BuildFeedbackURL 同样不签名）
	}
	if sig == "" || exp == "" {
		return errors.New("缺少签名参数")
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > ts {
		return errors.New("链接已过期（有效期 7 天）")
	}
	mac := hmac.New(sha256.New, []byte(s.token))
	// 拼接格式与 notifier.BuildFeedbackURL 逐字节对称（%d|%s|%d，exp 为 int64）
	fmt.Fprintf(mac, "%d|%s|%d", postID, action, ts)
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return errors.New("签名无效")
	}
	return nil
}

// handleFeedback 反馈链接（/f?post=&action=&exp=&sig=）：校验 → 写反馈 → 结果页
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	postID, err := strconv.ParseInt(q.Get("post"), 10, 64)
	action := q.Get("action")
	if err != nil || postID <= 0 || (action != models.FeedbackUseful && action != models.FeedbackUseless) {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if err := s.verifyFeedbackSig(postID, action, q.Get("exp"), q.Get("sig")); err != nil {
		s.renderFeedbackResult(w, "反馈链接无效或已过期（有效期 7 天），请联系管理员", false)
		return
	}
	if err := s.db.InsertFeedback(models.Feedback{PostID: postID, Channel: "", Action: action, CreatedAt: time.Now()}); err != nil {
		slog.Error("写反馈失败", "post_id", postID, "action", action, "err", err)
		s.renderFeedbackResult(w, "写入失败，请稍后重试", false)
		return
	}
	slog.Info("feedback_recorded", "post_id", postID, "action", action)
	s.renderFeedbackResult(w, "感谢反馈！已记录（该链接 7 天内有效）", true)
}

// renderFeedbackResult 简单结果页（内联 HTML，标题+文案）
func (s *Server) renderFeedbackResult(w http.ResponseWriter, msg string, ok bool) {
	title := "反馈结果"
	if ok {
		title = "反馈成功"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>%s</title></head><body><h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
}
