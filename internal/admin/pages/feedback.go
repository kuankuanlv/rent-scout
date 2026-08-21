package pages

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/security/actionref"
)

// verifyFeedbackSig 校验反馈链接签名（规格 7.1）。
// token 空（鉴权关闭）= 链接不签名，直接放行；有签名则 constant-time 校验 + exp 过期检查
func (h *PostsHandler) verifyFeedbackSig(postID int64, action, exp, sig string) error {
	token := h.opts.RT.Get().Admin.Token
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

func (h *PostsHandler) postIDFromRefQuery(q url.Values) (int64, error) {
	p := strings.TrimSpace(q.Get("p"))
	if p == "" {
		return 0, errors.New("缺少引用")
	}
	return actionref.Open(p, h.opts.RT.Get().Admin.Token)
}

// handleFeedback 反馈链接（/f?p=&action=&exp=&sig=）：校验 → 写反馈 → 结果页
func (h *PostsHandler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	postID, err := h.postIDFromRefQuery(q)
	action := q.Get("action")
	if err != nil || postID <= 0 || (action != models.FeedbackUseful && action != models.FeedbackUseless) {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	// 开关为准：鉴权关闭时跳过签名校验
	if !h.opts.RT.Get().Admin.AuthRequired {
		// 放行：不验证签名，直接处理
	} else if err := h.verifyFeedbackSig(postID, action, q.Get("exp"), q.Get("sig")); err != nil {
		h.renderFeedbackResult(w, "反馈链接无效或已过期（有效期 7 天），请联系管理员", false)
		return
	}
	if err := h.opts.DB.AddUserFeedback(postID, action, ""); err != nil {
		pkglog.Component(pkglog.Admin).Error("反馈写入失败", "post_id", postID, "action", action, "err", err)
		h.renderFeedbackResult(w, "写入失败，请稍后重试", false)
		return
	}
	pkglog.Component(pkglog.Admin).Info("反馈已记录", "post_id", postID, "action", action)
	h.renderFeedbackResult(w, "感谢反馈！已记录（该链接 7 天内有效）", true)
}

// handleHandledLink 已处理签名入口（GET /h?post=&exp=&sig=）：验签 → MarkPostHandled → 结果页；不写 feedbacks（Spec 09 §3.4）
func (h *PostsHandler) handleHandledLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	postID, err := h.postIDFromRefQuery(q)
	if err != nil || postID <= 0 {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	const action = "handled"
	if !h.opts.RT.Get().Admin.AuthRequired {
		// 鉴权关：跳过签名，直接标记
	} else if err := h.verifyFeedbackSig(postID, action, q.Get("exp"), q.Get("sig")); err != nil {
		h.renderHandledResult(w, "已处理链接无效或已过期（有效期 7 天），请联系管理员", false)
		return
	}
	if err := h.opts.DB.MarkPostHandled(postID); err != nil {
		pkglog.Component(pkglog.Admin).Error("已处理链接写入失败", "post_id", postID, "err", err)
		h.renderHandledResult(w, "写入失败，请稍后重试", false)
		return
	}
	pkglog.Component(pkglog.Admin).Info("已处理链接已生效", "post_id", postID)
	h.renderHandledResult(w, "已标记为已处理（该链接 7 天内有效）", true)
}

// handleFeedbacks 反馈写入接口（POST /api/feedbacks，规格 7.1）。
// 鉴权：带 query sig → HMAC 校验（卡片链接场景）；否则走管理 token（auth 中间件）
func (h *PostsHandler) handleFeedbacks(w http.ResponseWriter, r *http.Request) {
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
	if !h.opts.RT.Get().Admin.AuthRequired {
		// 鉴权关闭：一律放行（开关为准，不验证）
	} else if q.Get("sig") != "" {
		// 带签名：HMAC 校验（卡片链接场景）
		if err := h.verifyFeedbackSig(in.PostID, in.Action, q.Get("exp"), q.Get("sig")); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		// 无签名：auth 中间件已校验管理 token
	}
	if in.PostID <= 0 || (in.Action != "useful" && in.Action != "useless") {
		http.Error(w, "post_id/action 无效", http.StatusBadRequest)
		return
	}
	if err := h.opts.DB.AddUserFeedback(in.PostID, in.Action, in.Reason); err != nil {
		pkglog.Component(pkglog.Admin).Error("写反馈失败", "post_id", in.PostID, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// renderHandledResult 已处理结果页（内联 HTML）
func (h *PostsHandler) renderHandledResult(w http.ResponseWriter, msg string, ok bool) {
	title := "已处理失败"
	if ok {
		title = "已处理成功"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>%s</title></head><body><h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
}

// renderFeedbackResult 简单结果页（内联 HTML，标题+文案）
func (h *PostsHandler) renderFeedbackResult(w http.ResponseWriter, msg string, ok bool) {
	title := "反馈结果"
	if ok {
		title = "反馈成功"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><title>%s</title></head><body><h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(msg))
}
