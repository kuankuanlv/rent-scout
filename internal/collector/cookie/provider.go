package cookie

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

// Provider cookie 获取层：按源返回可用 cookie。
// 三种实现：none（匿名）/ raw（配置原文）/ cookiecloud（同步）
type Provider interface {
	Get(ctx context.Context, source string) (string, error)
}

// New 按 cookie_mode 选择实现；raw 读 cfg.CookieRaw
func New(mode string, cfg config.DoubanCookieConfig) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none":
		return noneProvider{}, nil
	case "raw":
		return rawProvider{cookie: cfg.CookieRaw}, nil
	case "cookiecloud":
		return newCookiecloudProvider(cfg), nil
	default:
		return nil, fmt.Errorf("未知 cookie_mode: %q（仅 none/raw/cookiecloud）", mode)
	}
}

// NewHotConfigProvider 每次 Get 从 HotConfig 读最新敏感配置再建具体 provider
func NewHotConfigProvider(hc *config.HotConfig) Provider {
	return hotConfigProvider{hc: hc}
}

type hotConfigProvider struct {
	hc *config.HotConfig
}

func (p hotConfigProvider) Get(ctx context.Context, source string) (string, error) {
	if p.hc == nil {
		return "", nil
	}
	dc := p.hc.Secrets().Collector.Douban
	mode := strings.ToLower(strings.TrimSpace(dc.CookieMode))
	inner, err := New(dc.CookieMode, dc)
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
