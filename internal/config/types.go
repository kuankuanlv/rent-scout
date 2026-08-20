package config

import (
	"rent-scout/internal/config/admin"
	"rent-scout/internal/config/collector"
	"rent-scout/internal/config/filter"
	"rent-scout/internal/config/general"
	"rent-scout/internal/config/notifier"
)

type ServerConfig = general.ServerConfig
type LogConfig = general.LogConfig
type CollectorConfig = collector.Config
type DoubanConfig = collector.DoubanConfig
type WeiboConfig = collector.WeiboConfig
type DoubanCookieConfig = collector.DoubanCookieConfig
type SecretsCollector = collector.SecretsCollector
type FilterConfig = filter.Config
type LLMConfig = filter.LLMConfig
type SecretsFilter = filter.SecretsFilter
type NotifierConfig = notifier.Config
type SecretsNotifier = notifier.SecretsNotifier
type WebhookSecretConfig = notifier.WebhookSecretConfig
type DingtalkConfig = notifier.DingtalkConfig
type PushplusConfig = notifier.PushplusConfig
type ServerchanConfig = notifier.ServerchanConfig
type CustomWebhookConfig = notifier.CustomWebhookConfig
type AdminConfig = admin.Config
type CookieMode = collector.CookieMode
type LLMAPIStyle = filter.LLMAPIStyle

type AppConfig struct {
	Server    ServerConfig
	Log       LogConfig
	Collector CollectorConfig
	Filter    FilterConfig
	Notifier  NotifierConfig
	Admin     AdminConfig
}

type Secrets struct {
	Collector SecretsCollector
	Filter    SecretsFilter
	Notifier  SecretsNotifier
}

const (
	CookieModeNone        = collector.CookieModeNone
	CookieModeRaw         = collector.CookieModeRaw
	CookieModeCookieCloud = collector.CookieModeCookieCloud

	LLMStyleNone   = filter.LLMStyleNone
	LLMStyleOpenAI = filter.LLMStyleOpenAI
	LLMStyleOther  = filter.LLMStyleOther

	KeyDoubanCookieMode     = collector.KeyDoubanCookieMode
	KeyDoubanCookieRaw      = collector.KeyDoubanCookieRaw
	KeyDoubanCookieCloudURL = collector.KeyDoubanCookieCloudURL
	KeyDoubanCookieCloudKey = collector.KeyDoubanCookieCloudKey
	KeyDoubanCookieCloudPwd = collector.KeyDoubanCookieCloudPwd

	KeyWeiboCookieMode     = collector.KeyWeiboCookieMode
	KeyWeiboCookieRaw      = collector.KeyWeiboCookieRaw
	KeyWeiboCookieRawCN    = collector.KeyWeiboCookieRawCN
	KeyWeiboCookieCloudURL = collector.KeyWeiboCookieCloudURL
	KeyWeiboCookieCloudKey = collector.KeyWeiboCookieCloudKey
	KeyWeiboCookieCloudPwd = collector.KeyWeiboCookieCloudPwd

	DefaultLogMemoryLines   = general.DefaultLogMemoryLines
	MinLogMemoryLines       = general.MinLogMemoryLines
	MaxLogMemoryLines       = general.MaxLogMemoryLines
	DefaultNotifierBatch    = notifier.DefaultNotifierBatch
	DefaultNotifierInterval = notifier.DefaultNotifierInterval
	DefaultLLMBaseURL       = filter.DefaultLLMBaseURL
	DefaultLLMModel         = filter.DefaultLLMModel
)

var DefaultDoubanGroups = collector.DefaultDoubanGroups

func ParseCookieMode(s string) CookieMode         { return collector.ParseCookieMode(s) }
func CookieSource(source string) string           { return collector.CookieSource(source) }
func CookieCloudDomain(source string) string      { return collector.CookieCloudDomain(source) }
func CookieModeKey(source string) string          { return collector.CookieModeKey(source) }
func CookieRawKey(source string) string           { return collector.CookieRawKey(source) }
func CookieCloudURLKey(source string) string      { return collector.CookieCloudURLKey(source) }
func CookieCloudKeyKey(source string) string      { return collector.CookieCloudKeyKey(source) }
func CookieCloudPwdKey(source string) string      { return collector.CookieCloudPwdKey(source) }
func ParseLLMAPIStyle(s string) LLMAPIStyle       { return filter.ParseLLMAPIStyle(s) }

func DefaultApp() *AppConfig {
	cfg := &AppConfig{}
	applyDefaults(cfg)
	return cfg
}

func DefaultSecrets() *Secrets {
	return &Secrets{
		Collector: SecretsCollector{
			Douban: DoubanCookieConfig{CookieMode: CookieModeNone.String()},
			Weibo:  DoubanCookieConfig{CookieMode: CookieModeNone.String()},
		},
		Filter: SecretsFilter{
			LLM: LLMConfig{
				BaseURL:  DefaultLLMBaseURL,
				Model:    DefaultLLMModel,
				APIStyle: LLMStyleOpenAI.String(),
			},
		},
	}
}

func applyDefaults(cfg *AppConfig) {
	general.ApplyDefaults(&cfg.Server, &cfg.Log)
	collector.ApplyDefaults(&cfg.Collector)
	filter.ApplyDefaults(&cfg.Filter)
	notifier.ApplyDefaults(&cfg.Notifier)
	admin.ApplyDefaults(&cfg.Admin)
}

