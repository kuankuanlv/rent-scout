package admin

import (
	"fmt"
	"net/http"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// handleNotifyTest POST /admin/config/notify/test：用草稿试发最近一批帖，不写通知账本
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := probeTimeout(r)
	if !ok {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer cancel()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		_ = r.ParseForm()
	}
	if s.notifyProbe == nil {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：探测未配置"})
		return
	}
	ch := notifyProbeChannel(r)
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

	limit := 2

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

func notifyProbeChannel(r *http.Request) string {
	for _, k := range []string{"channel", "notify_channel"} {
		v := strings.ToLower(strings.TrimSpace(r.FormValue(k)))
		if v == "feishu" || v == "pushplus" {
			return v
		}
	}
	return strings.ToLower(strings.TrimSpace(r.FormValue("channel")))
}

func (s *Server) draftNotifySecrets(r *http.Request) (webhook, token, topic string) {
	stored := s.rt.Secrets().Notifier
	webhook = firstNonEmpty(
		r.FormValue("secret.notifier.feishu.webhook"),
		r.FormValue("feishu_webhook"),
	)
	if webhook == "" || webhook == "••••••••" {
		webhook = stored.Feishu.Webhook
	}
	token = firstNonEmpty(
		r.FormValue("secret.notifier.pushplus.token"),
		r.FormValue("pushplus_token"),
	)
	if token == "" || token == "••••••••" {
		token = stored.Pushplus.Token
	}
	topic = firstNonEmpty(
		r.FormValue("secret.notifier.pushplus.topic"),
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
		for _, t := range p.Tags {
			if t.Kind == models.TagKindLocation && strings.TrimSpace(t.Text) != "" {
				tag = t.Text
				break
			}
		}
		item := NotifyProbeItem{
			PostID:             p.ID,
			Title:              p.Title,
			URL:                p.URL,
			AddressTag:         tag,
			Price:              models.PriceYuan(p.Price),
				FeedbackURL:        notifier.AbsActionURL(origin, notifier.BuildFeedbackURL(p.ID, "useful", secret)),
				FeedbackUselessURL: notifier.AbsActionURL(origin, notifier.BuildFeedbackURL(p.ID, "useless", secret)),
				HandledURL:         notifier.AbsActionURL(origin, notifier.BuildFeedbackURL(p.ID, "handled", secret)),
			}
			if models.HasContact(p.Contact) {
				item.Contact = p.Contact
			}
			if fr, ok, err := s.db.FilterResultByPostID(p.ID); err == nil && ok && fr.AI != nil {
				if item.Price <= 0 {
					item.Price = fr.AI.Price
				}
				if item.Contact == "" && models.HasContact(fr.AI.Contact) {
					item.Contact = fr.AI.Contact
				}
				item.Commuting = fr.AI.Commuting
				item.Reason = fr.AI.Reason
			}
			items = append(items, item)
		}
		return items
	}

	func mockNotifyPosts() []models.RentPost {
	return []models.RentPost{
		{
			ID: -1, Source: "douban", Title: "【连通检测】望京一居示例",
			URL:  "https://www.douban.com/group/topic/mock-probe-1/",
			Tags: []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}},
		},
		{
			ID: -2, Source: "douban", Title: "【连通检测】梨园次卧示例",
			URL:  "https://www.douban.com/group/topic/mock-probe-2/",
			Tags: []models.PostTag{{Kind: models.TagKindLocation, Text: "梨园", Source: models.TagSourceSystem}},
		},
	}
}
