package cookie

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// 风控关键字（含「非」「人类」「异常请求」等常见片段）
var riskKeywords = []string{
	"检测到有异常请求",
	"有异常请求",
	"异常请求",
	"禁止访问",
	"非人类",
	"不是人类",
	"turing.captcha",
}

// RiskDetected 风控检测：响应体含异常关键字即触发
func RiskDetected(body string) bool {
	return RiskSnippet(body) != ""
}

// RiskSnippet 从响应体抠一段含风控关键字的原文摘要（无 cookie；限长）
func RiskSnippet(body string) string {
	if body == "" {
		return ""
	}
	lower := body // 中文关键字直接 Contains
	for _, kw := range riskKeywords {
		idx := strings.Index(lower, kw)
		if idx < 0 {
			continue
		}
		start := idx - 24
		if start < 0 {
			start = 0
		}
		end := idx + len(kw) + 48
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
	return ""
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

// OnlineProbe 在线探测入口（默认 ProbeDoubanOnline；单测可替换）
var OnlineProbe = ProbeDoubanOnline

// ProbeDoubanOnline 带 Cookie 对豆瓣首页做一次轻量 GET（超时 ≤8s）；无明文返回
func ProbeDoubanOnline(ctx context.Context, cookie string, client *http.Client) (ok bool, status, detail string) {
	return ProbeDoubanOnlineURL(ctx, "https://www.douban.com/", cookie, client)
}

// ProbeDoubanOnlineURL 同上，可指定 URL（单测注入本地 server）
func ProbeDoubanOnlineURL(ctx context.Context, rawURL, cookie string, client *http.Client) (ok bool, status, detail string) {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, "error", err.Error()
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "error", err.Error()
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return false, "error", err.Error()
	}
	body := string(b)
	if snip := RiskSnippet(body); snip != "" {
		return false, "risk", "响应含风控关键字：" + snip
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "error", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, "ok", "探测成功"
}
