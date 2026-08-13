package cookie

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"rent-scout/internal/pkglog"
)

// 风控关键字：无 cookie 时豆瓣页原文是「有异常请求从你的 IP 发出，请 登录 使用豆瓣」
// 「请 登录」中间常有空格或 <a>；长句优先，避免只抠到「异常请求」
var riskKeywords = []string{
	"有异常请求从你的 IP 发出，请 登录 使用豆瓣",
	"有异常请求从你的 IP 发出",
	"请 登录 使用豆瓣",
	"请登录使用豆瓣",
	"检测到有异常请求",
	"有异常请求",
	"异常请求",
	"禁止访问",
	"非人类",
	"不是人类",
	"请证明你是人类",
	"turing.captcha",
	"sec.douban.com",
}

// RiskDetected 风控检测：响应体含异常关键字即触发
func RiskDetected(body string) bool {
	return RiskSnippet(body) != ""
}

// RiskSnippet 从响应体抠一段含风控关键字的原文摘要（无 cookie；限长）
// 先在去标签可见文本里匹配（「请 登录」常夹链接）；再压掉标点匹配间隔符
func RiskSnippet(body string) string {
	if body == "" {
		return ""
	}
	visible := visibleText(body)
	for _, hay := range []string{visible, body} {
		if hay == "" {
			continue
		}
		for _, kw := range riskKeywords {
			if idx := strings.Index(hay, kw); idx >= 0 {
				return clipRiskSnippet(hay, idx, len(kw))
			}
		}
	}
	compactBody := compactLetters(visible)
	if compactBody == "" {
		compactBody = compactLetters(body)
	}
	for _, kw := range riskKeywords {
		ck := compactLetters(kw)
		if ck == "" || !strings.Contains(compactBody, ck) {
			continue
		}
		return "匹配「" + kw + "」（原文可能含间隔符）"
	}
	return ""
}

// visibleText 去掉 HTML 标签，空白压成单空格，方便对上「请 登录」
func visibleText(html string) string {
	var b strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func clipRiskSnippet(body string, idx, matchLen int) string {
	start := idx - 24
	if start < 0 {
		start = 0
	}
	end := idx + matchLen + 48
	if end > len(body) {
		end = len(body)
	}
	snip := strings.TrimSpace(body[start:end])
	snip = strings.Join(strings.Fields(snip), " ")
	if utf8.RuneCountInString(snip) > 120 {
		runes := []rune(snip)
		snip = string(runes[:120]) + "…"
	}
	return snip
}

// compactLetters 只留字母数字（小写），用于忽略间隔符的风控匹配
func compactLetters(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// ParseCookieRough 粗检：非空且含 =
func ParseCookieRough(cookie string) (ok bool, cookieLen int, previewMasked string) {
	cookie = strings.TrimSpace(cookie)
	cookieLen = len(cookie)
	previewMasked = MaskCookiePreview(cookie)
	if cookie == "" || !strings.Contains(cookie, "=") {
		return false, cookieLen, previewMasked
	}
	return true, cookieLen, previewMasked
}

// CookieHeaderNames 从 "k=v; k=v" 串抽出 cookie 名
func CookieHeaderNames(cookie string) []string {
	var names []string
	seen := map[string]bool{}
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, _, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// CookieHeaderPreviews 从 cookie 头串抽出 name=脱敏 value（探测展示用）
func CookieHeaderPreviews(cookie string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, val, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if !ok {
			out = append(out, name)
			continue
		}
		out = append(out, name+"="+MaskCookiePreview(strings.TrimSpace(val)))
	}
	return out
}

// MaskCookiePreview 脱敏：首尾各最多保留 8 个字符，中间用 …
func MaskCookiePreview(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	n := utf8.RuneCountInString(s)
	if n <= 8 {
		return s
	}
	runes := []rune(s)
	head, tail := 8, 8
	if n <= head+tail {
		head = n / 2
		if head == 0 {
			head = 1
		}
		tail = n - head
		if tail < 1 {
			tail = 1
			head = n - 1
		}
	}
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

// DoubanPageResult 豆瓣页探测：通过或 HTTP + 可见文本摘要
type DoubanPageResult struct {
	OK      bool
	HTTP    int
	Snippet string
}

// ProbePage 管理台豆瓣检测入口（单测可替换）
var ProbePage = probeDoubanPage

// OnlineProbe 在线探测入口（默认 ProbeDoubanOnline；单测可替换）
var OnlineProbe = ProbeDoubanOnline

// OnlineProbeURL 可指定 URL 的探测入口（单测可替换；管理台优先打小组列表）
var OnlineProbeURL = ProbeDoubanOnlineURL

// ProbeDoubanOnline 带 Cookie 对豆瓣首页做一次轻量 GET（超时 ≤8s）；无明文返回
func ProbeDoubanOnline(ctx context.Context, cookie string, client *http.Client) (ok bool, status, detail string) {
	return ProbeDoubanOnlineURL(ctx, "https://www.douban.com/", cookie, client)
}

// ProbeDoubanOnlineURL 同上，可指定 URL（单测注入本地 server）
func ProbeDoubanOnlineURL(ctx context.Context, rawURL, cookie string, client *http.Client) (ok bool, status, detail string) {
	page := probeDoubanPage(ctx, rawURL, cookie, client)
	if page.OK {
		return true, "ok", "探测成功"
	}
	if page.HTTP == 0 {
		return false, "error", page.Snippet
	}
	if RiskDetected(page.Snippet) {
		return false, "risk", page.Snippet
	}
	return false, "error", fmt.Sprintf("HTTP %d", page.HTTP)
}

func probeDoubanPage(ctx context.Context, rawURL, cookieHdr string, client *http.Client) DoubanPageResult {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return DoubanPageResult{Snippet: err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if cookieHdr != "" {
		req.Header.Set("Cookie", cookieHdr)
	}
	resp, err := client.Do(req)
	if err != nil {
		pkglog.ProbeHTTPErr(pkglog.Admin, "douban_probe", http.MethodGet, rawURL, req.Header, err)
		return DoubanPageResult{Snippet: err.Error()}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return DoubanPageResult{HTTP: resp.StatusCode, Snippet: err.Error()}
	}
	pkglog.ProbeHTTP(pkglog.Admin, "douban_probe", http.MethodGet, rawURL, req.Header, nil, resp.StatusCode, resp.Header, b)
	body := string(b)
	snippet := clipVisible(body, 400)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300 && RiskSnippet(body) == ""
	stage := "fail"
	if ok {
		stage = "ok"
	}
	pkglog.Component(pkglog.Admin).Info("豆瓣探测判定",
		"stage", stage,
		"status", resp.StatusCode,
		"cookie_names", CookieHeaderNames(cookieHdr),
	)
	return DoubanPageResult{OK: ok, HTTP: resp.StatusCode, Snippet: snippet}
}

func clipVisible(html string, n int) string {
	s := visibleText(html)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
