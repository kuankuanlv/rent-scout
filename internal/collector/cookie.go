package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

// CookieProvider cookie 获取层（规格 §5）：按源返回可用 cookie。
// 四种实现：none（匿名）/ raw（配置原文）/ file（本地文件）/ cookiecloud（同步）
type CookieProvider interface {
	Get(ctx context.Context, source string) (string, error)
}

// NewCookieProvider 按 cookie_mode 选择实现；file 需传文件路径；raw 读 cfg.CookieRaw
func NewCookieProvider(mode, cookieFile string, cfg config.DoubanCookieConfig) (CookieProvider, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none":
		return noneProvider{}, nil
	case "raw":
		return rawProvider{cookie: cfg.CookieRaw}, nil
	case "file":
		return fileProvider{path: cookieFile}, nil
	case "cookiecloud":
		return newCookiecloudProvider(cfg), nil
	default:
		return nil, fmt.Errorf("未知 cookie_mode: %q", mode)
	}
}

// NewRuntimeCookieProvider 每次 Get 从 Runtime 读最新敏感配置再建具体 provider
func NewRuntimeCookieProvider(rt *config.Runtime) CookieProvider {
	return runtimeCookieProvider{rt: rt}
}

type runtimeCookieProvider struct {
	rt *config.Runtime
}

func (p runtimeCookieProvider) Get(ctx context.Context, source string) (string, error) {
	if p.rt == nil {
		return "", nil
	}
	dc := p.rt.GetEnv().Collector.Douban
	mode := strings.ToLower(strings.TrimSpace(dc.CookieMode))
	inner, err := NewCookieProvider(dc.CookieMode, dc.CookieFile, dc)
	if err != nil {
		return "", err
	}
	cookie, err := inner.Get(ctx, source)
	if err != nil {
		return "", err
	}
	// mode 期望有 cookie 却拿到空串：记一次空降级（无明文）
	if cookie == "" && mode != "" && mode != "none" {
		pkglog.Component(pkglog.Collector).Info("[cookie_get_empty] Cookie 为空", "source", source, "mode", mode)
	}
	return cookie, nil
}

// noneProvider 匿名访问
type noneProvider struct{}

func (noneProvider) Get(ctx context.Context, source string) (string, error) {
	return "", nil
}

// rawProvider 直接返回配置里的 cookie 原文
type rawProvider struct {
	cookie string
}

func (p rawProvider) Get(ctx context.Context, source string) (string, error) {
	return strings.TrimSpace(p.cookie), nil
}

// fileProvider 读本地 cookie 文件（手动导出/脚本更新，即改即用）；
// 文件缺失/读失败 → 降级匿名（不阻断采集，规格 4.4）
type fileProvider struct {
	path string
}

func (p fileProvider) Get(ctx context.Context, source string) (string, error) {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return "", nil // 降级匿名
	}
	return strings.TrimSpace(string(b)), nil
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
	if riskDetected(body) {
		return false, "risk", "响应含风控关键字"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "error", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, "ok", "探测成功"
}
