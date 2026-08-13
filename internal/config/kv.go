package config

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	KeySetupCompleted = "setup.completed"
	// EmptySentinel 表单显式清空配置项时提交此标记，入库为空串
	EmptySentinel = "__EMPTY__"
)

// DefaultApp 内置默认公开配置（SQLite 空库时使用）
func DefaultApp() *AppConfig {
	cfg := &AppConfig{}
	applyDefaults(cfg)
	return cfg
}

// DefaultEnv 内置默认敏感配置（全空 = 能力关闭）
func DefaultEnv() *EnvLocalConfig {
	return &EnvLocalConfig{
		Collector: EnvCollector{
			Douban: DoubanCookieConfig{CookieMode: "none"},
		},
	}
}

// NormalizeValue 把表单 sentinel 归一化为存储值
func NormalizeValue(v string) string {
	if v == EmptySentinel {
		return ""
	}
	return v
}

// AppToKV 公开配置扁平化为 KV
func AppToKV(cfg *AppConfig) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	ai := "true"
	if cfg.Filter.AIEnabled != nil && !*cfg.Filter.AIEnabled {
		ai = "false"
	}
	auth := "false"
	if cfg.Admin.AuthRequired {
		auth = "true"
	}
	kv := map[string]string{
		"server.addr":                  cfg.Server.Addr,
		"log.level":                    cfg.Log.Level,
		"log.format":                   cfg.Log.Format,
		"log.path":                     cfg.Log.Path,
		"pipeline.batch_size":          strconv.Itoa(cfg.Pipeline.BatchSize),
		"pipeline.linger_interval":     strconv.Itoa(cfg.Pipeline.LingerInterval),
		"collector.sources":            strings.Join(cfg.Collector.Sources, ","),
		"collector.interval":           strconv.Itoa(cfg.Collector.Interval),
		"collector.jitter_ratio":       fmt.Sprintf("%g", cfg.Collector.JitterRatio),
		"collector.max_age_days":       strconv.Itoa(cfg.Collector.MaxAgeDays),
		"collector.douban.groups":      strings.Join(cfg.Collector.Douban.Groups, "\n"),
		"collector.douban.interval":    strconv.Itoa(cfg.Collector.Douban.Interval),
		"filter.ai_enabled":            ai,
		"filter.ai_batch_size":         strconv.Itoa(cfg.Filter.AIBatchSize),
		"notifier.max_attempts":        strconv.Itoa(cfg.Notifier.MaxAttempts),
		"notifier.retry_base_interval": strconv.Itoa(cfg.Notifier.RetryBaseInterval),
		"notifier.channels":            strings.Join(cfg.Notifier.Channels, ","),
		"admin.auth_required":          auth,
		"admin.token":                  cfg.Admin.Token,
	}
	for src, n := range cfg.Filter.TrimLimits {
		kv["filter.trim_limits."+src] = strconv.Itoa(n)
	}
	return kv
}

// EnvToKV 敏感配置扁平化为 KV（secret. 前缀）
func EnvToKV(env *EnvLocalConfig) map[string]string {
	if env == nil {
		return map[string]string{}
	}
	dc := env.Collector.Douban
	llm := env.Filter.LLM
	n := env.Notifier
	cookieMode := dc.CookieMode
	if cookieMode == "" {
		cookieMode = "none"
	}
	return map[string]string{
		"secret.collector.douban.cookie_mode":          cookieMode,
		"secret.collector.douban.cookie_raw":           dc.CookieRaw,
		"secret.collector.douban.cookie_file":          dc.CookieFile,
		"secret.collector.douban.cookiecloud_url":      dc.CookiecloudURL,
		"secret.collector.douban.cookiecloud_key":      dc.CookiecloudKey,
		"secret.collector.douban.cookiecloud_password": dc.CookiecloudPass,
		"secret.filter.llm.api_key":                    llm.APIKey,
		"secret.filter.llm.base_url":                   llm.BaseURL,
		"secret.filter.llm.model":                      llm.Model,
		"secret.filter.llm.fallback_models":            strings.Join(llm.FallbackModels, ","),
		"secret.notifier.feishu.webhook":               n.Feishu.Webhook,
		"secret.notifier.dingtalk.webhook":             n.Dingtalk.Webhook,
		"secret.notifier.dingtalk.secret":              n.Dingtalk.Secret,
		"secret.notifier.wecom.webhook":                n.Wecom.Webhook,
		"secret.notifier.pushplus.token":               n.Pushplus.Token,
		"secret.notifier.serverchan.sendkey":           n.Serverchan.Sendkey,
		"secret.notifier.webhook.url":                  n.Webhook.URL,
		"secret.notifier.webhook.template":             n.Webhook.Template,
	}
}

// SectionKeys 各配置分区包含的 key（分块 submit 用）
var SectionKeys = map[string][]string{
	"general": {
		"server.addr", "log.level", "log.format", "log.path",
		"pipeline.batch_size", "pipeline.linger_interval",
	},
	"collector": {
		"collector.sources", "collector.interval", "collector.jitter_ratio", "collector.max_age_days",
		"collector.douban.groups", "collector.douban.interval",
		"secret.collector.douban.cookie_mode", "secret.collector.douban.cookie_raw",
		"secret.collector.douban.cookie_file",
		"secret.collector.douban.cookiecloud_url", "secret.collector.douban.cookiecloud_key",
		"secret.collector.douban.cookiecloud_password",
	},
	"filter": {
		"filter.ai_enabled", "filter.ai_batch_size", "filter.trim_limits.douban",
		"secret.filter.llm.api_key", "secret.filter.llm.base_url", "secret.filter.llm.model",
		"secret.filter.llm.fallback_models",
	},
	"notifier": {
		"notifier.max_attempts", "notifier.retry_base_interval", "notifier.channels",
		"secret.notifier.feishu.webhook",
	},
	"admin": {
		"admin.auth_required", "admin.token",
	},
}

// KVToApp 从 KV 还原公开配置（缺省走 applyDefaults）
func KVToApp(kv map[string]string) *AppConfig {
	cfg := DefaultApp()
	if len(kv) == 0 {
		return cfg
	}
	if v := kv["server.addr"]; v != "" {
		cfg.Server.Addr = v
	}
	if v := kv["log.level"]; v != "" {
		cfg.Log.Level = v
	}
	if v := kv["log.format"]; v != "" {
		cfg.Log.Format = v
	}
	cfg.Log.Path = kv["log.path"]
	if v := kv["pipeline.batch_size"]; v != "" {
		cfg.Pipeline.BatchSize = atoi(v, cfg.Pipeline.BatchSize)
	}
	if v := kv["pipeline.linger_interval"]; v != "" {
		cfg.Pipeline.LingerInterval = atoi(v, cfg.Pipeline.LingerInterval)
	}
	if v := kv["collector.sources"]; v != "" {
		cfg.Collector.Sources = splitComma(v)
	}
	if v := kv["collector.interval"]; v != "" {
		cfg.Collector.Interval = atoi(v, cfg.Collector.Interval)
	}
	if v := kv["collector.jitter_ratio"]; v != "" {
		cfg.Collector.JitterRatio = atof(v, cfg.Collector.JitterRatio)
	}
	if v := kv["collector.max_age_days"]; v != "" {
		cfg.Collector.MaxAgeDays = atoi(v, cfg.Collector.MaxAgeDays)
	}
	if v := kv["collector.douban.groups"]; v != "" {
		cfg.Collector.Douban.Groups = splitLines(v)
	}
	if v := kv["collector.douban.interval"]; v != "" {
		cfg.Collector.Douban.Interval = atoi(v, 0)
	}
	if v, ok := kv["filter.ai_enabled"]; ok && v != "" {
		b := strings.EqualFold(v, "true") || v == "1" || v == "on"
		cfg.Filter.AIEnabled = &b
	}
	if v := kv["filter.ai_batch_size"]; v != "" {
		cfg.Filter.AIBatchSize = atoi(v, cfg.Filter.AIBatchSize)
	}
	limits := map[string]int{}
	for k, v := range kv {
		if strings.HasPrefix(k, "filter.trim_limits.") && v != "" {
			limits[strings.TrimPrefix(k, "filter.trim_limits.")] = atoi(v, 800)
		}
	}
	if len(limits) > 0 {
		cfg.Filter.TrimLimits = limits
	}
	if v := kv["notifier.max_attempts"]; v != "" {
		cfg.Notifier.MaxAttempts = atoi(v, cfg.Notifier.MaxAttempts)
	}
	if v := kv["notifier.retry_base_interval"]; v != "" {
		cfg.Notifier.RetryBaseInterval = atoi(v, cfg.Notifier.RetryBaseInterval)
	}
	if v := kv["notifier.channels"]; v != "" {
		cfg.Notifier.Channels = splitComma(v)
	}
	if v, ok := kv["admin.auth_required"]; ok && v != "" {
		cfg.Admin.AuthRequired = strings.EqualFold(v, "true") || v == "1" || v == "on"
	}
	cfg.Admin.Token = kv["admin.token"]
	applyDefaults(cfg)
	return cfg
}

// KVToEnv 从 KV 还原敏感配置
func KVToEnv(kv map[string]string) *EnvLocalConfig {
	env := DefaultEnv()
	if len(kv) == 0 {
		return env
	}
	cookieMode := kv["secret.collector.douban.cookie_mode"]
	if cookieMode == "" {
		cookieMode = "none"
	}
	env.Collector.Douban = DoubanCookieConfig{
		CookieMode:      cookieMode,
		CookieRaw:       kv["secret.collector.douban.cookie_raw"],
		CookieFile:      kv["secret.collector.douban.cookie_file"],
		CookiecloudURL:  kv["secret.collector.douban.cookiecloud_url"],
		CookiecloudKey:  kv["secret.collector.douban.cookiecloud_key"],
		CookiecloudPass: kv["secret.collector.douban.cookiecloud_password"],
	}
	env.Filter.LLM = LLMConfig{
		APIKey:         kv["secret.filter.llm.api_key"],
		BaseURL:        kv["secret.filter.llm.base_url"],
		Model:          kv["secret.filter.llm.model"],
		FallbackModels: splitComma(kv["secret.filter.llm.fallback_models"]),
	}
	env.Notifier = EnvNotifier{
		Feishu:     WebhookSecretConfig{Webhook: kv["secret.notifier.feishu.webhook"]},
		Dingtalk:   DingtalkConfig{Webhook: kv["secret.notifier.dingtalk.webhook"], Secret: kv["secret.notifier.dingtalk.secret"]},
		Wecom:      WebhookSecretConfig{Webhook: kv["secret.notifier.wecom.webhook"]},
		Pushplus:   PushplusConfig{Token: kv["secret.notifier.pushplus.token"]},
		Serverchan: ServerchanConfig{Sendkey: kv["secret.notifier.serverchan.sendkey"]},
		Webhook:    CustomWebhookConfig{URL: kv["secret.notifier.webhook.url"], Template: kv["secret.notifier.webhook.template"]},
	}
	return env
}

// MergeKV 合并两个 KV map（后者覆盖前者）
func MergeKV(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func atof(s string, def float64) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return def
	}
	return n
}
