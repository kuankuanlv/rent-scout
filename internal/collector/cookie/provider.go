package cookie

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

// Provider cookie 获取层：按源返回可用 cookie。
// 采集路径只读本地：none 空串；raw / cookiecloud 都读 CookieRaw，绝不打 CookieCloud。
type Provider interface {
	Get(ctx context.Context, source string) (string, error)
}

// New 按 cookie_mode 选择实现；cookiecloud 在采集侧等同 raw（只读已同步的 CookieRaw）
func New(mode string, cfg config.DoubanCookieConfig) (Provider, error) {
	m := config.ParseCookieMode(mode)
	switch m {
	case config.CookieModeNone:
		return noneProvider{}, nil
	case config.CookieModeRaw, config.CookieModeCookieCloud:
		return rawProvider{cookie: cfg.CookieRaw}, nil
	default:
		return nil, fmt.Errorf("未知 cookie_mode: %q（仅 %s/%s/%s）", mode,
			config.CookieModeNone, config.CookieModeRaw, config.CookieModeCookieCloud)
	}
}

// NewHotConfigProvider 每次 Get 从 HotConfig 读最新敏感配置；采集不碰 CookieCloud
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
	mode := config.ParseCookieMode(dc.CookieMode)
	if !mode.Valid() {
		return "", fmt.Errorf("未知 cookie_mode: %q", dc.CookieMode)
	}
	if mode == config.CookieModeNone {
		return "", nil
	}
	ck := strings.TrimSpace(dc.CookieRaw)
	if ck == "" {
		pkglog.Component(pkglog.SourceCollector(source)).Error("本地 cookie 为空，本轮结束",
			"source", source, "mode", mode)
		return "", ErrCookieMissing
	}
	return ck, nil
}

type noneProvider struct{}

func (noneProvider) Get(ctx context.Context, source string) (string, error) {
	return "", nil
}

type rawProvider struct {
	cookie string
}

func (p rawProvider) Get(ctx context.Context, source string) (string, error) {
	return strings.TrimSpace(p.cookie), nil
}
