package admin

import (
	"context"

	"rent-scout/internal/config"
)

// SourceController 采集源控制接口（admin 不依赖 collector 包）
type SourceController interface {
	SetEnabled(name string, on bool) error
	Trigger(name string) error
	Sources() []string
	SourceEnabled(name string) bool
}

// CookieCloudInspect CookieCloud 探测结果（handler 不引用 cookie 包类型）
type CookieCloudInspect struct {
	Cookie      string
	Names       []string
	Previews    []string
	Algo        string
	CipherField string
	HTTPStatus  int
	Domains     []string
}

// DoubanPageResult 豆瓣页探测结果
type DoubanPageResult struct {
	OK      bool
	HTTP    int
	Snippet string
}

// CookieProbe 管理台 CookieCloud / 豆瓣探测
type CookieProbe interface {
	InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (CookieCloudInspect, error)
	ProbePage(ctx context.Context, probeURL, rawCookie string) DoubanPageResult
}

// LLMProbe 管理台 LLM 连通与模型列表
type LLMProbe interface {
	ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error)
	Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error)
}

// NotifyProbeItem 试发用的通知条目（不引用 notifier 包）
type NotifyProbeItem struct {
	PostID             int64
	Title              string
	URL                string
	Price              int
	Contact            string
	Commuting          string
	Reason             string
	AddressTag         string
	FeedbackURL        string
	FeedbackUselessURL string
	HandledURL         string
}

// NotifyProbe 管理台飞书/PushPlus 试发
type NotifyProbe interface {
	Send(ctx context.Context, channel, webhook, token, topic string, items []NotifyProbeItem) error
}
