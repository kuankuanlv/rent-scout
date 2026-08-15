package service

import (
	"context"

	"rent-scout/internal/admin"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/filter/llm"
)

type cookieProbe struct{}

func NewCookieProbe() admin.CookieProbe { return cookieProbe{} }

func (cookieProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig) (admin.CookieCloudInspect, error) {
	ins, err := cookie.InspectCookieCloud(ctx, draft)
	return admin.CookieCloudInspect{
		Cookie:      ins.Cookie,
		Names:       ins.Names,
		Previews:    ins.Previews,
		Algo:        ins.Algo,
		CipherField: ins.CipherField,
		HTTPStatus:  ins.HTTPStatus,
		Domains:     ins.Domains,
	}, err
}

func (cookieProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) admin.DoubanPageResult {
	page := cookie.ProbePage(ctx, probeURL, rawCookie, nil)
	return admin.DoubanPageResult{OK: page.OK, HTTP: page.HTTP, Snippet: page.Snippet}
}

type llmProbe struct{}

func NewLLMProbe() admin.LLMProbe { return llmProbe{} }

func (llmProbe) ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error) {
	c := llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true})
	return c.ListModels(ctx)
}

func (llmProbe) Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	c := llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true})
	return c.Chat(ctx, system, user)
}
