package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

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

	stored := s.rt.GetEnv().Collector.Douban
	draft := config.DoubanCookieConfig{
		CookieMode: mode,
		CookieRaw: firstNonEmpty(
			r.PostFormValue("cookie_raw"),
			r.PostFormValue("secret.collector.douban.cookie_raw"),
		),
		CookieFile: firstNonEmpty(
			r.PostFormValue("cookie_file"),
			r.PostFormValue("secret.collector.douban.cookie_file"),
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
	if draft.CookieFile == "" {
		draft.CookieFile = stored.CookieFile
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

	type parsePart struct {
		OK            bool   `json:"ok"`
		CookieLen     int    `json:"cookie_len"`
		PreviewMasked string `json:"preview_masked"`
		Status        string `json:"status,omitempty"`
		Detail        string `json:"detail,omitempty"`
	}
	type onlinePart struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}

	resp := map[string]any{}
	var parse parsePart
	var online onlinePart

	if mode == "none" {
		parse = parsePart{OK: true, Status: "skipped", Detail: "匿名模式"}
		online = onlinePart{OK: true, Status: "skipped", Detail: "匿名模式，跳过在线探测"}
		resp["ok"] = true
		resp["parse"] = parse
		resp["online"] = online
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "skipped")
		writeJSON(w, resp)
		return
	}

	provider, err := collector.NewCookieProvider(mode, draft.CookieFile, draft)
	if err != nil {
		parse = parsePart{OK: false, Detail: err.Error()}
		online = onlinePart{OK: false, Status: "skipped", Detail: "解析失败，跳过在线"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "error")
		writeJSON(w, resp)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	cookie, err := provider.Get(ctx, "douban")
	if err != nil {
		parse = parsePart{OK: false, Detail: err.Error()}
		online = onlinePart{OK: false, Status: "skipped", Detail: "获取 cookie 失败"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", 0, "status", "error")
		writeJSON(w, resp)
		return
	}

	okParse, cookieLen, masked := collector.ParseCookieRough(cookie)
	parse = parsePart{OK: okParse, CookieLen: cookieLen, PreviewMasked: masked}
	if !okParse {
		parse.Detail = "cookie 为空或不含 ="
		online = onlinePart{OK: false, Status: "skipped", Detail: "解析未通过，跳过在线"}
		resp["ok"] = false
		resp["parse"] = parse
		resp["online"] = online
		pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", cookieLen, "status", "parse_fail")
		writeJSON(w, resp)
		return
	}

	onlineOK, onlineStatus, onlineDetail := collector.OnlineProbe(ctx, cookie, nil)
	online = onlinePart{OK: onlineOK, Status: onlineStatus, Detail: onlineDetail}
	resp["ok"] = onlineOK
	resp["parse"] = parse
	resp["online"] = online
	pkglog.Component(pkglog.Admin).Info("[cookie_test] Cookie 探测", "cookie_len", cookieLen, "status", onlineStatus)
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
