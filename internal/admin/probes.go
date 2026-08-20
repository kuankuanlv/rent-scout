package admin

import (
	"context"
	"fmt"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/filter/ai/llm"
	"rent-scout/internal/notifier"
	"rent-scout/internal/notifier/channels"
)

type cookieProbe struct{}

func NewCookieProbe() CookieProbe { return cookieProbe{} }

func (cookieProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (CookieCloudInspect, error) {
	ins, err := cookie.InspectCookieCloudFor(ctx, draft, source)
	return CookieCloudInspect{
		Cookie:      ins.Cookie,
		Names:       ins.Names,
		Previews:    ins.Previews,
		Algo:        ins.Algo,
		CipherField: ins.CipherField,
		HTTPStatus:  ins.HTTPStatus,
		Domains:     ins.Domains,
	}, err
}

func (cookieProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) DoubanPageResult {
	page := cookie.ProbePage(ctx, probeURL, rawCookie, nil)
	return DoubanPageResult{OK: page.OK, HTTP: page.HTTP, Snippet: page.Snippet}
}

type llmProbe struct{}

func NewLLMProbe() LLMProbe { return llmProbe{} }

func (llmProbe) ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error) {
	c := llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true})
	return c.ListModels(ctx)
}

func (llmProbe) Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	c := llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true})
	return c.Chat(ctx, system, user)
}

type notifyProbe struct{}

func NewNotifyProbe() NotifyProbe { return notifyProbe{} }

func (notifyProbe) Send(ctx context.Context, channel, webhook, token, topic string, items []NotifyProbeItem) error {
	nitems := make([]notifier.NotifyItem, len(items))
	for i, it := range items {
		nitems[i] = notifier.NotifyItem{
			PostID:             it.PostID,
			Title:              it.Title,
			URL:                it.URL,
			Price:              it.Price,
			Contact:            it.Contact,
			Commuting:          it.Commuting,
			Reason:             it.Reason,
			AddressTag:         it.AddressTag,
			FeedbackURL:        it.FeedbackURL,
			FeedbackUselessURL: it.FeedbackUselessURL,
			HandledURL:         it.HandledURL,
		}
	}
	ch := channels.Build(channel, config.SecretsNotifier{
		Feishu:   config.WebhookSecretConfig{Webhook: webhook},
		Pushplus: config.PushplusConfig{Token: token, Topic: topic},
	})
	if ch == nil {
		return fmt.Errorf("不支持的渠道或密钥缺失")
	}
	_, _, err := ch.Send(ctx, nitems)
	return err
}
