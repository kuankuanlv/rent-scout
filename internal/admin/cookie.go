package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

type cookieParsePart struct {
	OK            bool   `json:"ok"`
	CookieLen     int    `json:"cookie_len"`
	PreviewMasked string `json:"preview_masked"`
	Status        string `json:"status,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type cookieOnlinePart struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// handleCookieTest POST /admin/config/cookie/test：草稿探测，不写库（规格 §5.3）
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
		mode = "none"
	}

	stored := s.rt.Secrets().Collector.Douban
	draft := config.DoubanCookieConfig{
		CookieMode: mode,
		CookieRaw: firstNonEmpty(
			r.PostFormValue("cookie_raw"),
			r.PostFormValue("secret.collector.douban.cookie_raw"),
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
	// raw / cookiecloud 密码：草稿空则沿用已存
	if draft.CookieRaw == "" {
		draft.CookieRaw = stored.CookieRaw
	}
	if draft.CookiecloudURL == "" {
		draft.CookiecloudURL = stored.CookiecloudURL
	}
	if draft.CookiecloudKey == "" {
		draft.CookiecloudKey = stored.CookiecloudKey
	}
	if draft.CookiecloudPass == "" {
		draft.CookiecloudPass = stored.CookiecloudPass
	}

	resp := map[string]any{}
	var parse cookieParsePart
	var online cookieOnlinePart
	var ccNames []string

	if mode == "none" {
		parse = cookieParsePart{OK: true, Status: "skipped", Detail: "匿名模式"}
		online = cookieOnlinePart{OK: true, Status: "skipped", Detail: "匿名模式，跳过在线探测"}
		resp["ok"] = true
		resp["parse"] = parse
		resp["online"] = online
		resp["summary"] = "✅ 成功（匿名/跳过在线探测）"
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "skipped")
		writeJSON(w, resp)
		return
	}

	if mode == "file" {
		parse = cookieParsePart{OK: false, Detail: "cookie_mode=file 已移除，请改用 none/raw/cookiecloud"}
		online = cookieOnlinePart{OK: false, Status: "skipped", Detail: "不支持 file 草稿"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		resp["summary"] = "❌ 失败：cookie_mode=file 已移除"
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "error")
		writeJSON(w, resp)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var rawCookie string
	var err error
	if mode == "cookiecloud" {
		rawCookie, ccNames, err = cookie.InspectCookieCloud(ctx, draft)
	} else {
		provider, perr := cookie.New(mode, draft)
		if perr != nil {
			err = perr
		} else {
			rawCookie, err = provider.Get(ctx, "douban")
			ccNames = cookie.CookieHeaderNames(rawCookie)
		}
	}
	if err != nil {
		parse = cookieParsePart{OK: false, Detail: err.Error()}
		online = cookieOnlinePart{OK: false, Status: "skipped", Detail: "获取 cookie 失败"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		if len(ccNames) > 0 {
			resp["cookiecloud_cookies"] = ccNames
		}
		resp["summary"] = "❌ 失败：" + err.Error()
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "error")
		writeJSON(w, resp)
		return
	}

	okParse, cookieLen, masked := cookie.ParseCookieRough(rawCookie)
	parse = cookieParsePart{OK: okParse, CookieLen: cookieLen, PreviewMasked: masked}
	if len(ccNames) == 0 {
		ccNames = cookie.CookieHeaderNames(rawCookie)
	}
	if len(ccNames) > 0 {
		resp["cookiecloud_cookies"] = ccNames
	}
	if !okParse {
		parse.Detail = "cookie 为空或不含 ="
		online = cookieOnlinePart{OK: false, Status: "skipped", Detail: "解析未通过，跳过在线"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		resp["summary"] = "❌ 失败：Cookie 解析失败" + cookieNamesHint(ccNames)
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", cookieLen, "status", "parse_fail")
		writeJSON(w, resp)
		return
	}

	onlineOK, onlineStatus, onlineDetail := cookie.OnlineProbe(ctx, rawCookie, nil)
	online = cookieOnlinePart{OK: onlineOK, Status: onlineStatus, Detail: onlineDetail}
	resp["ok"] = onlineOK
	resp["parse"] = parse
	resp["online"] = online
	resp["summary"] = cookieTestSummary(onlineOK, onlineStatus, onlineDetail, parse, ccNames)
	pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", cookieLen, "status", onlineStatus, "cookie_names", len(ccNames))
	writeJSON(w, resp)
}

func cookieNamesHint(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) > 8 {
		names = names[:8]
	}
	return "（命中：" + strings.Join(names, ", ") + "）"
}

func cookieTestSummary(ok bool, status, detail string, parse cookieParsePart, names []string) string {
	hint := cookieNamesHint(names)
	if ok && (status == "skipped" || status == "ok") {
		if status == "skipped" {
			return "✅ 成功（匿名/跳过在线探测）" + hint
		}
		return "✅ 成功：Cookie 可用" + hint
	}
	if status == "risk" || status == "captcha" || strings.Contains(detail, "风控") {
		return "⚠️ 风控：" + detail + hint
	}
	if !parse.OK {
		return "❌ 失败：" + firstNonEmpty(parse.Detail, "Cookie 解析失败") + hint
	}
	return "❌ 失败：" + firstNonEmpty(detail, status, "探测未通过") + hint
}
