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
	rangeFrom := CanonicalDayOffset(cfg.Collector.Douban.RangeFrom)
	if rangeFrom == "" {
		rangeFrom = "-10"
	}
	rangeTo := CanonicalDayOffset(cfg.Collector.Douban.RangeTo)
	if rangeTo == "" {
		rangeTo = "now"
	}
	doubanInterval := cfg.Collector.Douban.Interval
	if doubanInterval <= 0 {
		doubanInterval = 3
	}
	kv := map[string]string{
		"server.addr":                  cfg.Server.Addr,
		"server.public_base":           cfg.Server.PublicBase,
		"log.level":                    cfg.Log.Level,
		"log.format":                   cfg.Log.Format,
		"log.path":                     cfg.Log.Path,
		"log.memory_lines":             strconv.Itoa(cfg.Log.MemoryLines),
		"collector.sources":            strings.Join(cfg.Collector.Sources, ","),
		"collector.interval":           strconv.Itoa(cfg.Collector.Interval),
		"collector.jitter_ratio":       fmt.Sprintf("%g", cfg.Collector.JitterRatio),
		"collector.max_age_days":       strconv.Itoa(cfg.Collector.MaxAgeDays),
		"collector.douban.groups":      strings.Join(cfg.Collector.Douban.Groups, "\n"),
		"collector.douban.interval":    strconv.Itoa(doubanInterval),
		"collector.douban.range_from":  rangeFrom,
		"collector.douban.range_to":    rangeTo,
		"filter.ai_enabled":            ai,
		"filter.batch_size":            strconv.Itoa(cfg.Filter.BatchSize),
		"filter.ai_batch_size":         strconv.Itoa(cfg.Filter.AIBatchSize),
		"notifier.max_attempts":        strconv.Itoa(cfg.Notifier.MaxAttempts),
		"notifier.retry_base_interval": strconv.Itoa(cfg.Notifier.RetryBaseInterval),
		"notifier.batch_size":          strconv.Itoa(cfg.Notifier.BatchSize),
		"notifier.channels":            strings.Join(cfg.Notifier.Channels, ","),
		"admin.auth_required":          auth,
		"admin.token":                  cfg.Admin.Token,
	}
	return kv
}

// SecretsToKV 敏感配置扁平化为 KV（secret. 前缀）
func SecretsToKV(sec *Secrets) map[string]string {
	if sec == nil {
		return map[string]string{}
	}
	dc := sec.Collector.Douban
	llm := sec.Filter.LLM
	n := sec.Notifier
	cookieMode := ParseCookieMode(dc.CookieMode).String()
	apiStyle := ParseLLMAPIStyle(llm.APIStyle).String()
	if apiStyle == "" {
		apiStyle = LLMStyleOpenAI.String()
	}
	return map[string]string{
		KeyDoubanCookieMode:                   cookieMode,
		KeyDoubanCookieRaw:                    dc.CookieRaw,
		"secret.collector.douban.cookie_file": dc.CookieFile,
		KeyDoubanCookieCloudURL:               dc.CookiecloudURL,
		KeyDoubanCookieCloudKey:               dc.CookiecloudKey,
		KeyDoubanCookieCloudPwd:               dc.CookiecloudPass,
		"secret.filter.llm.api_key":           llm.APIKey,
		"secret.filter.llm.base_url":          llm.BaseURL,
		"secret.filter.llm.model":             llm.Model,
		"secret.filter.llm.fallback_models":   strings.Join(llm.FallbackModels, ","),
		"secret.filter.llm.api_style":         apiStyle,
		"secret.notifier.feishu.webhook":      n.Feishu.Webhook,
		"secret.notifier.dingtalk.webhook":    n.Dingtalk.Webhook,
		"secret.notifier.dingtalk.secret":     n.Dingtalk.Secret,
		"secret.notifier.wecom.webhook":       n.Wecom.Webhook,
		"secret.notifier.pushplus.token":      n.Pushplus.Token,
		"secret.notifier.pushplus.topic":      n.Pushplus.Topic,
		"secret.notifier.serverchan.sendkey":  n.Serverchan.Sendkey,
		"secret.notifier.webhook.url":         n.Webhook.URL,
		"secret.notifier.webhook.template":    n.Webhook.Template,
	}
}

// SectionKeys 各配置分区包含的 key（分块 submit 用）
var SectionKeys = map[string][]string{
	"general": {
		"server.addr", "server.public_base", "log.level", "log.format", "log.path", "log.memory_lines",
	},
	"collector": {
		"collector.sources", "collector.interval", "collector.jitter_ratio", "collector.max_age_days",
		"collector.douban.groups", "collector.douban.interval",
		"collector.douban.range_from", "collector.douban.range_to",
		"secret.collector.douban.cookie_mode", "secret.collector.douban.cookie_raw",
		"secret.collector.douban.cookie_file",
		"secret.collector.douban.cookiecloud_url", "secret.collector.douban.cookiecloud_key",
		"secret.collector.douban.cookiecloud_password",
	},
	"filter": {
		"filter.ai_enabled", "filter.batch_size", "filter.ai_batch_size",
		"secret.filter.llm.api_style",
		"secret.filter.llm.api_key", "secret.filter.llm.base_url", "secret.filter.llm.model",
	},
	"notifier": {
		"notifier.max_attempts", "notifier.retry_base_interval", "notifier.batch_size", "notifier.channels",
		"secret.notifier.feishu.webhook",
		"secret.notifier.pushplus.token", "secret.notifier.pushplus.topic",
	},
	"admin": {
		"admin.auth_required", "admin.token",
	},
}

// KVToApp 从 KV 还原公开配置（缺省走 applyDefaults）
func KVToApp(kv map[string]string) *AppConfig {
	cfg := &AppConfig{}
	if len(kv) == 0 {
		applyDefaults(cfg)
		return cfg
	}
	if v := kv["server.addr"]; v != "" {
		cfg.Server.Addr = v
	}
	cfg.Server.PublicBase = kv["server.public_base"]
	if v := kv["log.level"]; v != "" {
		cfg.Log.Level = v
	}
	if v := kv["log.format"]; v != "" {
		cfg.Log.Format = v
	}
	cfg.Log.Path = kv["log.path"]
	if v := kv["log.memory_lines"]; v != "" {
		cfg.Log.MemoryLines = atoi(v, 0)
	}
	// 旧 pipeline.* 仅作迁移 fallback；新 key 优先（BatchSize 仍为 0 时 applyDefaults 会回落）
	if v := kv["pipeline.batch_size"]; v != "" {
		cfg.Pipeline.BatchSize = atoi(v, 0)
	}
	if v := kv["pipeline.linger_interval"]; v != "" {
		cfg.Pipeline.LingerInterval = atoi(v, 0)
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
	if v, ok := kv["collector.douban.range_from"]; ok {
		cfg.Collector.Douban.RangeFrom = CanonicalDayOffset(v)
	}
	if v, ok := kv["collector.douban.range_to"]; ok {
		cfg.Collector.Douban.RangeTo = CanonicalDayOffset(v)
	}
	if v, ok := kv["filter.ai_enabled"]; ok && v != "" {
		b := strings.EqualFold(v, "true") || v == "1" || v == "on"
		cfg.Filter.AIEnabled = &b
	}
	if v := kv["filter.batch_size"]; v != "" {
		cfg.Filter.BatchSize = atoi(v, cfg.Filter.BatchSize)
	}
	if v := kv["filter.ai_batch_size"]; v != "" {
		cfg.Filter.AIBatchSize = atoi(v, cfg.Filter.AIBatchSize)
	}
	if v := kv["notifier.max_attempts"]; v != "" {
		cfg.Notifier.MaxAttempts = atoi(v, cfg.Notifier.MaxAttempts)
	}
	if v := kv["notifier.retry_base_interval"]; v != "" {
		cfg.Notifier.RetryBaseInterval = atoi(v, cfg.Notifier.RetryBaseInterval)
	}
	if v := kv["notifier.batch_size"]; v != "" {
		cfg.Notifier.BatchSize = atoi(v, cfg.Notifier.BatchSize)
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

// KVToSecrets 从 KV 还原敏感配置
func KVToSecrets(kv map[string]string) *Secrets {
	sec := DefaultSecrets()
	if len(kv) == 0 {
		return sec
	}
	cookieMode := kv[KeyDoubanCookieMode]
	if strings.EqualFold(cookieMode, "file") {
		cookieMode = CookieModeNone.String()
	} else {
		cookieMode = ParseCookieMode(cookieMode).String()
	}
	apiStyleLLM := ParseLLMAPIStyle(kv["secret.filter.llm.api_style"]).String()
	if apiStyleLLM == "" {
		apiStyleLLM = LLMStyleOpenAI.String()
	}
	sec.Collector.Douban = DoubanCookieConfig{
		CookieMode:      cookieMode,
		CookieRaw:       kv[KeyDoubanCookieRaw],
		CookieFile:      kv["secret.collector.douban.cookie_file"],
		CookiecloudURL:  kv[KeyDoubanCookieCloudURL],
		CookiecloudKey:  kv[KeyDoubanCookieCloudKey],
		CookiecloudPass: kv[KeyDoubanCookieCloudPwd],
	}
	sec.Filter.LLM = LLMConfig{
		APIKey:         kv["secret.filter.llm.api_key"],
		BaseURL:        kv["secret.filter.llm.base_url"],
		Model:          kv["secret.filter.llm.model"],
		FallbackModels: splitComma(kv["secret.filter.llm.fallback_models"]),
		APIStyle:       apiStyleLLM,
	}
	sec.Notifier = SecretsNotifier{
		Feishu:     WebhookSecretConfig{Webhook: kv["secret.notifier.feishu.webhook"]},
		Dingtalk:   DingtalkConfig{Webhook: kv["secret.notifier.dingtalk.webhook"], Secret: kv["secret.notifier.dingtalk.secret"]},
		Wecom:      WebhookSecretConfig{Webhook: kv["secret.notifier.wecom.webhook"]},
		Pushplus:   PushplusConfig{Token: kv["secret.notifier.pushplus.token"], Topic: kv["secret.notifier.pushplus.topic"]},
		Serverchan: ServerchanConfig{Sendkey: kv["secret.notifier.serverchan.sendkey"]},
		Webhook:    CustomWebhookConfig{URL: kv["secret.notifier.webhook.url"], Template: kv["secret.notifier.webhook.template"]},
	}
	return sec
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
