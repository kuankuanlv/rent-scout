package admin

import (
	"net/http"
	"net/url"
	"strconv"

	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// channelRow 渠道统计行：store.ChannelStat + Total（模板成功率分母 = sent+failed+dead）
type channelRow struct {
	store.ChannelStat
	Total int
}

// handleStats 统计报表 + 死信（GET /admin/stats）
// 页面数据 {Today, Channels, RuleStats, Dead, Token, Msg}：Token 透传鉴权 token，Msg 承载重发提示
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	today, err := s.db.TodayStats()
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("今日统计失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	channels, err := s.db.ChannelStats()
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("渠道统计失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	ruleStats, err := s.db.RuleHitStats()
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("规则命中统计失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	dead, err := s.db.FetchDeadNotifications(100)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("死信列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	rows := make([]channelRow, 0, len(channels))
	for _, c := range channels {
		rows = append(rows, channelRow{ChannelStat: c, Total: c.Sent + c.Failed + c.Dead})
	}
	if err := s.tmpl.ExecuteTemplate(w, "stats", mergePageCtx(s.pageCtx(r, "stats"), map[string]any{
		"Today": today, "Channels": rows, "RuleStats": ruleStats, "Dead": dead, "Msg": r.URL.Query().Get("msg"),
		"Onboard": collectOnboard(s.rt.Get(), s.rt.Secrets(), r.URL.Query().Get("token")),
	})); err != nil {
		pkglog.Component(pkglog.Admin).Error("模板渲染失败", "err", err)
	}
}

// handleDeadReset 死信重发（POST /admin/dead/reset：post_id/channel）→ ResetNotification
// 仅接受 POST：GET 等请求一律 405，防止 <a>/<img> 链接触发写库。
// 成功：slog.Info("[dead_reset] 死信已重置", ...) + 302 回 /admin/stats；false（非 dead 状态）→ 302 + 提示"该通知非死信"
func (s *Server) handleDeadReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	postID, _ := strconv.ParseInt(r.PostFormValue("post_id"), 10, 64)
	channel := r.PostFormValue("channel")
	if postID <= 0 || channel == "" {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	reset, err := s.db.ResetNotification(postID, channel)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("死信重发失败", "post_id", postID, "channel", channel, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	if !reset {
		// 非 dead 状态（幂等保护）：提示后仍回统计页
		pkglog.Component(pkglog.Admin).Info("死信重发跳过", "post_id", postID, "channel", channel)
		q := url.Values{}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		q.Set("msg", "该通知非死信")
		http.Redirect(w, r, "/admin/stats?"+q.Encode(), http.StatusSeeOther)
		return
	}
	pkglog.Component(pkglog.Admin).Info("死信已重置", "post_id", postID, "channel", channel)
	// PRG：防重复提交；鉴权开启时把 token 带回重定向目标，避免跳回后 401
	redirectTo := "/admin/stats"
	if tok := r.URL.Query().Get("token"); tok != "" {
		redirectTo += "?token=" + tok
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}
