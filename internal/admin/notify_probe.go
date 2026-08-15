package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/actionref"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

const notifyProbeMax = 10

// handleNotifyTest POST /admin/config/notify/test：用草稿试发最近一批帖，不写通知账本
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	if s.notifyProbe == nil {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：探测未配置"})
		return
	}
	ch := strings.ToLower(strings.TrimSpace(r.PostFormValue("channel")))
	if ch != "feishu" && ch != "pushplus" {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：仅支持飞书或 PushPlus"})
		return
	}
	webhook, token, topic := s.draftNotifySecrets(r)
	if ch == "feishu" && webhook == "" {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：请填写飞书 Webhook"})
		return
	}
	if ch == "pushplus" && token == "" {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：请填写 PushPlus Token"})
		return
	}

	limit := 5
	if s.rt != nil {
		if app := s.rt.Get(); app != nil && app.Notifier.BatchSize > 0 {
			limit = app.Notifier.BatchSize
		}
	}
	if limit > notifyProbeMax {
		limit = notifyProbeMax
	}

	posts, err := s.db.ListPosts(store.PostListFilter{}, limit, 0)
	if err != nil {
		pkglog.Component(pkglog.Admin).Info("通知连通检测", "stage", "list", "err", err)
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：" + err.Error()})
		return
	}
	mocked := false
	if len(posts) == 0 {
		posts = mockNotifyPosts()
		mocked = true
	}
	items := s.postsToProbeItems(posts)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.notifyProbe.Send(ctx, ch, webhook, token, topic, items); err != nil {
		pkglog.Component(pkglog.Admin).Info("通知连通检测", "stage", "send", "channel", ch, "err", err, "mocked", mocked, "count", len(items))
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：" + err.Error(), "mocked": mocked, "count": len(items)})
		return
	}
	summary := fmt.Sprintf("已发送 %d 条", len(items))
	if mocked {
		summary += "（库内无帖，用了内置样例）"
	} else {
		summary += "（最近入库，不限筛选状态）"
	}
	pkglog.Component(pkglog.Admin).Info("通知连通检测", "stage", "ok", "channel", ch, "mocked", mocked, "count", len(items))
	writeJSON(w, map[string]any{"ok": true, "summary": summary, "mocked": mocked, "count": len(items)})
}

func (s *Server) draftNotifySecrets(r *http.Request) (webhook, token, topic string) {
	stored := s.rt.Secrets().Notifier
	webhook = firstNonEmpty(
		r.PostFormValue("secret.notifier.feishu.webhook"),
		r.PostFormValue("feishu_webhook"),
	)
	if webhook == "" || webhook == "••••••••" {
		webhook = stored.Feishu.Webhook
	}
	token = firstNonEmpty(
		r.PostFormValue("secret.notifier.pushplus.token"),
		r.PostFormValue("pushplus_token"),
	)
	if token == "" || token == "••••••••" {
		token = stored.Pushplus.Token
	}
	topic = firstNonEmpty(
		r.PostFormValue("secret.notifier.pushplus.topic"),
		stored.Pushplus.Topic,
	)
	return strings.TrimSpace(webhook), strings.TrimSpace(token), strings.TrimSpace(topic)
}

func (s *Server) postsToProbeItems(posts []models.RentPost) []NotifyProbeItem {
	secret := ""
	origin := ""
	if s.rt != nil {
		secret = s.rt.FeedbackSecret()
		origin = config.ResolvePublicOrigin(s.rt.Get())
	}
	items := make([]NotifyProbeItem, 0, len(posts))
	for _, p := range posts {
		tag := "未分组"
		if len(p.AddressTags) > 0 && strings.TrimSpace(p.AddressTags[0]) != "" {
			tag = p.AddressTags[0]
		}
		item := NotifyProbeItem{
			PostID:             p.ID,
			Title:              p.Title,
			URL:                p.URL,
			AddressTag:         tag,
			FeedbackURL:        absProbeURL(origin, probeFeedbackURL(p.ID, "useful", secret)),
			FeedbackUselessURL: absProbeURL(origin, probeFeedbackURL(p.ID, "useless", secret)),
			HandledURL:         absProbeURL(origin, probeFeedbackURL(p.ID, "handled", secret)),
		}
		if fr, ok, err := s.db.FilterResultByPostID(p.ID); err == nil && ok && fr.AI != nil {
			item.Price = fr.AI.Price
			item.Contact = fr.AI.Contact
			item.Commuting = fr.AI.Commuting
			item.Reason = fr.AI.Reason
		}
		items = append(items, item)
	}
	return items
}

func probeFeedbackURL(postID int64, action, secret string) string {
	if postID <= 0 {
		return "#"
	}
	ref := actionref.Seal(postID, secret)
	var base string
	if action == "handled" {
		base = fmt.Sprintf("/h?p=%s", ref)
	} else {
		base = fmt.Sprintf("/f?p=%s&action=%s", ref, action)
	}
	if secret == "" {
		return base
	}
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d|%s|%d", postID, action, exp)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s&exp=%d&sig=%s", base, exp, sig)
}

func absProbeURL(origin, path string) string {
	if origin == "" || path == "" || path == "#" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(origin, "/") + path
}

func mockNotifyPosts() []models.RentPost {
	return []models.RentPost{
		{
			ID: -1, Source: "douban", Title: "【连通检测】望京一居示例",
			URL:         "https://www.douban.com/group/topic/mock-probe-1/",
			AddressTags: []string{"望京"},
		},
		{
			ID: -2, Source: "douban", Title: "【连通检测】梨园次卧示例",
			URL:         "https://www.douban.com/group/topic/mock-probe-2/",
			AddressTags: []string{"梨园"},
		},
	}
}
