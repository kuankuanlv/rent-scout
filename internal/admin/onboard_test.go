package admin

import (
	"strings"
	"testing"

	"rent-scout/internal/admin/onboard"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

func TestCookiePasteHint(t *testing.T) {
	empty := onboard.CookiePasteHint("")
	if !strings.Contains(empty, "整段直接贴进来") {
		t.Errorf("空 cookie 应提示直接粘贴, got %q", empty)
	}
	saved := onboard.CookiePasteHint("bid=abc; dbcl2=xyz")
	if !strings.HasPrefix(saved, "已保存 · 长度 ") {
		t.Errorf("已保存 cookie 应给长度 hint, got %q", saved)
	}
}

func TestCollectorOnboardDefaultOff(t *testing.T) {
	h := onboard.CollectorOnboard(config.DefaultApp(), config.DefaultSecrets(), "")
	if !h.Show || h.Title != "还没开始采集" {
		t.Fatalf("默认源关闭应提示还没开始采集, got %+v", h)
	}
	if h.DismissKey != "collector-not-started" {
		t.Errorf("DismissKey = %q", h.DismissKey)
	}
	if !strings.Contains(h.Href, "/admin/config?tab=sources") {
		t.Errorf("href = %q", h.Href)
	}
}

func TestCollectorOnboardMissingCookie(t *testing.T) {
	app := config.DefaultApp()
	app.Collector.Sources = []string{models.SourceDouban.String()}
	env := config.DefaultSecrets()
	env.Collector.Douban.CookieMode = config.CookieModeRaw.String()
	h := onboard.CollectorOnboard(app, env, "tok")
	if !h.Show || h.Title != "采集源还缺 Cookie" {
		t.Fatalf("启用豆瓣但没 cookie 应提示, got %+v", h)
	}
	if h.DismissKey != "collector-missing-cookie" {
		t.Errorf("DismissKey = %q", h.DismissKey)
	}
	if !strings.Contains(h.Body, "豆瓣") {
		t.Errorf("body = %q", h.Body)
	}
	if !strings.Contains(h.Href, "token=tok") {
		t.Errorf("href 应带 token, got %q", h.Href)
	}
}

func TestCollectorOnboardReady(t *testing.T) {
	app := config.DefaultApp()
	app.Collector.Sources = []string{models.SourceDouban.String()}
	env := config.DefaultSecrets()
	env.Collector.Douban.CookieMode = config.CookieModeRaw.String()
	env.Collector.Douban.CookieRaw = "bid=1"
	h := onboard.CollectorOnboard(app, env, "")
	if h.Show {
		t.Fatalf("源已开且 cookie 已贴不应再提示, got %+v", h)
	}
}

func TestCollectOnboardIncludesAIAndNotify(t *testing.T) {
	hints := onboard.CollectOnboard(config.DefaultApp(), config.DefaultSecrets(), "")
	var titles []string
	for _, h := range hints {
		titles = append(titles, h.Title)
	}
	joined := strings.Join(titles, ",")
	if !strings.Contains(joined, "还没开始采集") || !strings.Contains(joined, "AI 审核还没钥匙") || !strings.Contains(joined, "还没开通知") {
		t.Errorf("首页清单缺项: %v", titles)
	}
}
