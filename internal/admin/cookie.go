package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

// handleCookieCloudTest POST /admin/config/cookiecloud/test：只测连通和解密，不打豆瓣
func (s *Server) handleCookieCloudTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	draft := s.draftCookieConfig(r)
	if strings.ToLower(draft.CookieMode) != "cookiecloud" {
		draft.CookieMode = "cookiecloud"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ins, err := cookie.InspectCookieCloud(ctx, draft)
	resp := map[string]any{
		"ok":      err == nil && ins.Cookie != "",
		"http":    ins.HTTPStatus,
		"algo":    ins.Algo,
		"field":   ins.CipherField,
		"domains": ins.Domains,
		"cookies": ins.Previews,
	}
	if len(ins.Names) > 0 {
		resp["cookie_names"] = ins.Names
	}
	if err != nil {
		resp["ok"] = false
		resp["summary"] = "失败：" + err.Error()
		pkglog.Component(pkglog.Admin).Info("CookieCloud 检测",
			"stage", "fail",
			"http", ins.HTTPStatus,
			"algo", ins.Algo,
			"err", err,
		)
		writeJSON(w, resp)
		return
	}
	resp["summary"] = "通过"
	pkglog.Component(pkglog.Admin).Info("CookieCloud 检测",
		"stage", "ok",
		"http", ins.HTTPStatus,
		"algo", ins.Algo,
		"cookies", ins.Names,
		"domains", ins.Domains,
	)
	writeJSON(w, resp)
}

// handleCookieTest POST /admin/config/cookie/test：用当前 Cookie 打豆瓣；通过或 HTTP+摘要
func (s *Server) handleCookieTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		r.PostFormValue("cookie_mode"),
		r.PostFormValue("secret.collector.douban.cookie_mode"),
	)))
	if mode == "" {
		mode = config.CookieModeNone.String()
	}
	if mode == "file" {
		writeJSON(w, map[string]any{"ok": false, "http": 0, "snippet": "cookie_mode=file 已移除，请改用 none/raw/cookiecloud"})
		return
	}

	draft := s.draftCookieConfig(r)
	draft.CookieMode = mode
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	rawCookie, err := fetchDraftCookie(ctx, mode, draft)
	if err != nil {
		pkglog.Component(pkglog.Admin).Info("豆瓣检测", "stage", "fetch_cookie", "mode", mode, "err", err)
		writeJSON(w, map[string]any{"ok": false, "http": 0, "summary": "失败：" + err.Error(), "snippet": err.Error()})
		return
	}

	probeURL := cookieProbeURL(r, s.rt)
	page := cookie.ProbePage(ctx, probeURL, rawCookie, nil)
	resp := map[string]any{"ok": page.OK, "http": page.HTTP}
	if page.OK {
		resp["summary"] = "通过"
	} else {
		resp["summary"] = "失败"
		if page.HTTP != 0 {
			resp["summary"] = "失败：HTTP " + strconv.Itoa(page.HTTP)
		}
	}
	if !page.OK && page.Snippet != "" {
		resp["snippet"] = page.Snippet
	}
	pkglog.Component(pkglog.Admin).Info("豆瓣检测",
		"stage", "page",
		"mode", mode,
		"ok", page.OK,
		"http", page.HTTP,
		"url", probeURL,
	)
	writeJSON(w, resp)
}

func (s *Server) draftCookieConfig(r *http.Request) config.DoubanCookieConfig {
	stored := s.rt.Secrets().Collector.Douban
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		r.PostFormValue("cookie_mode"),
		r.PostFormValue("secret.collector.douban.cookie_mode"),
		stored.CookieMode,
	)))
	if mode == "" {
		mode = "none"
	}
	draft := config.DoubanCookieConfig{
		CookieMode: mode,
		CookieRaw: firstNonEmpty(
			r.PostFormValue("cookie_raw"),
			r.PostFormValue("secret.collector.douban.cookie_raw"),
			stored.CookieRaw,
		),
		CookiecloudURL: firstNonEmpty(
			r.PostFormValue("cookiecloud_url"),
			r.PostFormValue("secret.collector.douban.cookiecloud_url"),
		),
		CookiecloudKey: firstNonEmpty(
			r.PostFormValue("cookiecloud_key"),
			r.PostFormValue("secret.collector.douban.cookiecloud_key"),
		),
		CookiecloudPass: firstNonEmpty(
			r.PostFormValue("cookiecloud_password"),
			r.PostFormValue("secret.collector.douban.cookiecloud_password"),
		),
	}
	return draft
}

func fetchDraftCookie(ctx context.Context, mode string, draft config.DoubanCookieConfig) (string, error) {
	m := config.ParseCookieMode(mode)
	if m == config.CookieModeNone {
		return "", nil
	}
	if m == config.CookieModeCookieCloud {
		ins, err := cookie.InspectCookieCloud(ctx, draft)
		if err != nil {
			return "", err
		}
		ck := strings.TrimSpace(ins.Cookie)
		if ck == "" {
			return "", cookie.ErrCookieMissing
		}
		return ck, nil
	}
	ck := strings.TrimSpace(draft.CookieRaw)
	if ck == "" {
		return "", cookie.ErrCookieMissing
	}
	return ck, nil
}

// cookieProbeURL 优先草稿/已存第一组小组 URL，否则豆瓣首页
func cookieProbeURL(r *http.Request, rt *config.HotConfig) string {
	raw := firstNonEmpty(
		r.PostFormValue("collector.douban.groups"),
		r.PostFormValue("groups"),
	)
	if line := firstGroupLine(raw); line != "" {
		return line
	}
	if rt != nil {
		if app := rt.Get(); app != nil {
			if groups := app.Collector.Douban.Groups; len(groups) > 0 {
				if g := strings.TrimSpace(groups[0]); g != "" {
					return g
				}
			}
		}
	}
	return "https://www.douban.com/"
}

func firstGroupLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
