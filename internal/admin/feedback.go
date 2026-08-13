package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

// verifyFeedbackSig 校验反馈链接签名（规格 7.1）。
// token 空（鉴权关闭）= 链接不签名，直接放行；有签名则 constant-time 校验 + exp 过期检查
func (s *Server) verifyFeedbackSig(postID int64, action, exp, sig string) error {
	token := s.rt.Get().Admin.Token
	if token == "" {
		return nil // 全开放场景（BuildFeedbackURL 同样不签名）
	}
	if sig == "" || exp == "" {
		return errors.New("缺少签名参数")
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > ts {
		return errors.New("链接已过期（有效期 7 天）")
	}
	mac := hmac.New(sha256.New, []byte(token))
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
	// 开关为准：鉴权关闭时跳过签名校验
	if !s.rt.Get().Admin.AuthRequired {
		// 放行：不验证签名，直接处理
	} else if err := s.verifyFeedbackSig(postID, action, q.Get("exp"), q.Get("sig")); err != nil {
		s.renderFeedbackResult(w, "反馈链接无效或已过期（有效期 7 天），请联系管理员", false)
		return
	}
	if err := s.db.InsertFeedback(models.Feedback{PostID: postID, Channel: "", Action: action, CreatedAt: time.Now()}); err != nil {
		pkglog.Component(pkglog.Admin).Error("[feedback_write_failed] 反馈写入失败", "post_id", postID, "action", action, "err", err)
		s.renderFeedbackResult(w, "写入失败，请稍后重试", false)
		return
	}
	pkglog.Component(pkglog.Admin).Info("[feedback_recorded] 反馈已记录", "post_id", postID, "action", action)
	s.renderFeedbackResult(w, "感谢反馈！已记录（该链接 7 天内有效）", true)
}

// handleHandledLink 已处理签名入口（GET /h?post=&exp=&sig=）：验签 → MarkPostHandled → 结果页；不写 feedbacks（Spec 09 §3.4）
func (s *Server) handleHandledLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	postID, err := strconv.ParseInt(q.Get("post"), 10, 64)
	if err != nil || postID <= 0 {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	const action = "handled"
	if !s.rt.Get().Admin.AuthRequired {
		// 鉴权关：跳过签名，直接标记
	} else if err := s.verifyFeedbackSig(postID, action, q.Get("exp"), q.Get("sig")); err != nil {
		s.renderHandledResult(w, "已处理链接无效或已过期（有效期 7 天），请联系管理员", false)
		return
	}
	if err := s.db.MarkPostHandled(postID); err != nil {
		pkglog.Component(pkglog.Admin).Error("[handled_link_write_failed] 已处理链接写入失败", "post_id", postID, "err", err)
		s.renderHandledResult(w, "写入失败，请稍后重试", false)
		return
	}
	pkglog.Component(pkglog.Admin).Info("[handled_link_recorded] 已处理链接已生效", "post_id", postID)
	s.renderHandledResult(w, "已标记为已处理（该链接 7 天内有效）", true)
}

// renderHandledResult 已处理结果页（内联 HTML）
func (s *Server) renderHandledResult(w http.ResponseWriter, msg string, ok bool) {
	title := "已处理失败"
	if ok {
		title = "已处理成功"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>%s</title></head><body><h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
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
