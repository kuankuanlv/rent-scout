package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/config/urls"
	"rent-scout/internal/pkglog"
)

// handleCookieCloudTest POST /admin/config/cookiecloud/test：只测连通和解密，不打豆瓣
func (s *Server) handleCookieCloudTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parseAdminForm(r); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	draft := s.draftCookieConfig(r)
	source := cookieSource(r)
	if strings.ToLower(draft.CookieMode) != "cookiecloud" {
		draft.CookieMode = "cookiecloud"
	}
	if strings.TrimSpace(draft.CookiecloudURL) == "" || strings.TrimSpace(draft.CookiecloudKey) == "" || strings.TrimSpace(draft.CookiecloudPass) == "" {
		writeJSON(w, map[string]any{"ok": false, "summary": "失败：请填写页面上的 CookieCloud 地址、UUID 和密码"})
		return
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
			"source", source,
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
		"source", source,
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
	if err := parseAdminForm(r); err != nil {
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
		"source", cookieSource(r),
		"mode", mode,
		"ok", page.OK,
		"http", page.HTTP,
		"url", probeURL,
	)
	writeJSON(w, resp)
}

func parseAdminForm(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		return r.ParseMultipartForm(2 << 20)
	}
	return r.ParseForm()
}

func cookieSource(r *http.Request) string {
	return config.CookieSource(firstNonEmpty(r.PostFormValue("source"), r.FormValue("source")))
}

func (s *Server) draftCookieConfig(r *http.Request) config.DoubanCookieConfig {
	source := cookieSource(r)
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		r.PostFormValue("cookie_mode"),
		r.PostFormValue(config.CookieModeKey(source)),
	)))
	if mode == "" {
		mode = "none"
	}
	return config.DoubanCookieConfig{
		CookieMode: mode,
		CookieRaw: firstNonEmpty(
			r.PostFormValue("cookie_raw"),
			r.PostFormValue(config.CookieRawKey(source)),
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
			r.PostFormValue("collector.weibo.supertopics"),
			r.PostFormValue("collector.weibo.users"),
			r.PostFormValue("urls"),
		)
		if ids := urls.WeiboContainerIDs(strings.Split(raw, "\n")); len(ids) > 0 {
			return "https://weibo.com/p/" + ids[0]
		}
		if uids := urls.WeiboUIDs(strings.Split(raw, "\n")); len(uids) > 0 {
			return "https://weibo.com/u/" + uids[0]
		}
		if rt != nil {
			if app := rt.Get(); app != nil {
				if ids := urls.WeiboContainerIDs(app.Collector.Weibo.SuperTopics); len(ids) > 0 {
					return "https://weibo.com/p/" + ids[0]
				}
				if uids := urls.WeiboUIDs(app.Collector.Weibo.Users); len(uids) > 0 {
					return "https://weibo.com/u/" + uids[0]
				}
			}
		}
		return "https://weibo.com/"
	}
	raw := firstNonEmpty(
		r.PostFormValue("collector.douban.groups"),
		r.PostFormValue("groups"),
	)
	if line := urls.FirstHTTPURL(raw); line != "" {
		return line
	}
	if rt != nil {
		if app := rt.Get(); app != nil {
			if urls := urls.HTTPURLs(app.Collector.Douban.Groups); len(urls) > 0 {
				return urls[0]
			}
		}
	}
	return "https://www.douban.com/"
}

const errCookieMissing = cookieMissingError("本地 cookie 为空")

type cookieMissingError string

func (e cookieMissingError) Error() string { return string(e) }
