package admin

import (
	"fmt"
	"net/url"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

// onboardHint 首次/未就绪提示：帖子页、首页、配置分区共用
type onboardHint struct {
	Show       bool
	Title      string
	Body       string
	Href       string
	LinkText   string
	DismissKey string // 帖子页「不再显示」localStorage 键值
}

func cookiePasteHint(raw string) string {
	if raw != "" {
		return fmt.Sprintf("已保存 · 长度 %d。要换新的就把整段 Cookie 贴进来覆盖；留空不改", len(raw))
	}
	return "把浏览器里复制的 Cookie 整段直接贴进来即可（开发者工具 → Application → Cookies，或控制台敲 document.cookie）"
}

func withToken(path, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return path
	}
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

func cookieReady(ck config.DoubanCookieConfig) bool {
	switch config.ParseCookieMode(ck.CookieMode) {
	case config.CookieModeRaw:
		return strings.TrimSpace(ck.CookieRaw) != ""
	case config.CookieModeCookieCloud:
		return strings.TrimSpace(ck.CookiecloudURL) != "" && strings.TrimSpace(ck.CookiecloudKey) != "" && strings.TrimSpace(ck.CookiecloudPass) != ""
	default:
		return false
	}
}

func sourceEnabled(app *config.AppConfig, name string) bool {
	if app == nil {
		return false
	}
	for _, s := range app.Collector.Sources {
		if s == name {
			return true
		}
	}
	return false
}

func collectorOnboard(app *config.AppConfig, env *config.Secrets, token string) onboardHint {
	href := withToken("/admin/config?tab=sources", token)
	if app == nil || len(app.Collector.Sources) == 0 {
		return onboardHint{
			Show: true, Title: "还没开始采集", DismissKey: "collector-not-started",
			Body: "豆瓣和微博默认是关的。先到配置里启用你要的源，再把浏览器里的 Cookie 整段贴进去（直接粘贴内容即可）。",
			Href: href, LinkText: "去配置采集",
		}
	}
	var missing []string
	if env != nil {
		if sourceEnabled(app, models.SourceDouban.String()) && !cookieReady(env.Collector.Douban) {
			missing = append(missing, "豆瓣")
		}
		if sourceEnabled(app, models.SourceWeibo.String()) && !cookieReady(env.Collector.Weibo) {
			missing = append(missing, "微博")
		}
	}
	if len(missing) == 0 {
		return onboardHint{}
	}
	return onboardHint{
		Show: true, Title: "采集源还缺 Cookie", DismissKey: "collector-missing-cookie",
		Body: strings.Join(missing, "、") + " 已启用，但还没贴 Cookie。登录对应网站后把 Cookie 整段贴进配置即可。",
		Href: href, LinkText: "去贴 Cookie",
	}
}

func aiOnboard(app *config.AppConfig, env *config.Secrets, token string) onboardHint {
	if app == nil || app.Filter.AIEnabled == nil || !*app.Filter.AIEnabled {
		return onboardHint{}
	}
	if env != nil && strings.TrimSpace(env.Filter.LLM.APIKey) != "" {
		return onboardHint{}
	}
	return onboardHint{
		Show: true, Title: "AI 审核还没钥匙",
		Body: "AI 审核已开，但还没填 API Key。填了 DeepSeek Key，过硬筛的帖才会打徽章、补月租和联系方式。",
		Href: withToken("/admin/config?tab=ai", token), LinkText: "去填 API Key",
	}
}

func notifyOnboard(app *config.AppConfig, token string) onboardHint {
	if app != nil && len(app.Notifier.Channels) > 0 {
		return onboardHint{}
	}
	return onboardHint{
		Show: true, Title: "还没开通知",
		Body: "默认不发通知。勾选飞书或 PushPlus，填上 Webhook / Token 后，通过的帖才会推到手机。",
		Href: withToken("/admin/config?tab=notifier", token), LinkText: "去配置通知",
	}
}

func collectOnboard(app *config.AppConfig, env *config.Secrets, token string) []onboardHint {
	var out []onboardHint
	for _, h := range []onboardHint{
		collectorOnboard(app, env, token),
		aiOnboard(app, env, token),
		notifyOnboard(app, token),
	} {
		if h.Show {
			out = append(out, h)
		}
	}
	return out
}

func onboardForTab(tab string, app *config.AppConfig, env *config.Secrets, token string) onboardHint {
	switch tab {
	case "sources":
		return collectorOnboard(app, env, token)
	case "ai":
		return aiOnboard(app, env, token)
	case "notifier":
		return notifyOnboard(app, token)
	default:
		return onboardHint{}
	}
}
