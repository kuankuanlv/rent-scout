package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	source := cookieSource(r)
	if strings.ToLower(draft.CookieMode) != "cookiecloud" {
		draft.CookieMode = "cookiecloud"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if s.cookieProbe == nil {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：探测未配置"})
		return
	}
	ins, err := s.cookieProbe.InspectCookieCloud(ctx, draft, source)
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
		r.PostFormValue(config.CookieModeKey(cookieSource(r))),
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

	rawCookie, err := s.fetchDraftCookie(ctx, mode, draft, cookieSource(r))
	if err != nil {
		pkglog.Component(pkglog.Admin).Info("Cookie 检测", "stage", "fetch_cookie", "mode", mode, "err", err)
		writeJSON(w, map[string]any{"ok": false, "http": 0, "summary": "失败：" + err.Error(), "snippet": err.Error()})
		return
	}

	if s.cookieProbe == nil {
		writeJSON(w, map[string]any{"ok": false, "http": 0, "summary": "失败：探测未配置"})
		return
	}
	probeURL := cookieProbeURL(r, s.rt)
	page := s.cookieProbe.ProbePage(ctx, probeURL, rawCookie)
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
	pkglog.Component(pkglog.Admin).Info("Cookie 检测",
		"stage", "page",
		"mode", mode,
		"ok", page.OK,
		"http", page.HTTP,
		"url", probeURL,
	)
	writeJSON(w, resp)
}

func cookieSource(r *http.Request) string {
	return config.CookieSource(firstNonEmpty(r.PostFormValue("source")))
}

func (s *Server) draftCookieConfig(r *http.Request) config.DoubanCookieConfig {
	source := cookieSource(r)
	stored := s.rt.Secrets().Collector.CookieFor(source)
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		r.PostFormValue("cookie_mode"),
		r.PostFormValue(config.CookieModeKey(source)),
		stored.CookieMode,
	)))
	if mode == "" {
		mode = "none"
	}
	draft := config.DoubanCookieConfig{
		CookieMode: mode,
		CookieRaw: firstNonEmpty(
			r.PostFormValue("cookie_raw"),
			r.PostFormValue(config.CookieRawKey(source)),
			stored.CookieRaw,
		),
		CookiecloudURL: firstNonEmpty(
			r.PostFormValue("cookiecloud_url"),
			r.PostFormValue(config.CookieCloudURLKey(source)),
		),
		CookiecloudKey: firstNonEmpty(
			r.PostFormValue("cookiecloud_key"),
			r.PostFormValue(config.CookieCloudKeyKey(source)),
		),
		CookiecloudPass: firstNonEmpty(
			r.PostFormValue("cookiecloud_password"),
			r.PostFormValue(config.CookieCloudPwdKey(source)),
		),
	}
	return draft
}

func (s *Server) fetchDraftCookie(ctx context.Context, mode string, draft config.DoubanCookieConfig, source string) (string, error) {
	m := config.ParseCookieMode(mode)
	if m == config.CookieModeNone {
		return "", nil
	}
	if m == config.CookieModeCookieCloud {
		ins, err := s.cookieProbe.InspectCookieCloud(ctx, draft, source)
		if err != nil {
			return "", err
		}
		ck := strings.TrimSpace(ins.Cookie)
		if ck == "" {
			return "", errCookieMissing
		}
		return ck, nil
	}
	ck := strings.TrimSpace(draft.CookieRaw)
	if ck == "" {
		return "", errCookieMissing
	}
	return ck, nil
}

// cookieProbeURL 优先草稿/已存第一组 URL，否则源首页
func cookieProbeURL(r *http.Request, rt *config.HotConfig) string {
	source := cookieSource(r)
	if source == "weibo" {
		raw := firstNonEmpty(
			r.PostFormValue("collector.weibo.urls"),
			r.PostFormValue("urls"),
		)
		if line := config.FirstHTTPURL(raw); line != "" {
			return line
		}
		if rt != nil {
			if app := rt.Get(); app != nil {
				if urls := config.HTTPURLs(app.Collector.Weibo.URLs); len(urls) > 0 {
					return urls[0]
				}
			}
		}
		return "https://weibo.com/"
	}
	raw := firstNonEmpty(
		r.PostFormValue("collector.douban.groups"),
		r.PostFormValue("groups"),
	)
	if line := config.FirstHTTPURL(raw); line != "" {
		return line
	}
	if rt != nil {
		if app := rt.Get(); app != nil {
			if urls := config.HTTPURLs(app.Collector.Douban.Groups); len(urls) > 0 {
				return urls[0]
			}
		}
	}
	return "https://www.douban.com/"
}

const errCookieMissing = cookieMissingError("本地 cookie 为空")

type cookieMissingError string

func (e cookieMissingError) Error() string { return string(e) }
