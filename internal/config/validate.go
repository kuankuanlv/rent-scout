package config

import (
	"strings"
)

// ValidateApp 校验公开配置，返回错误消息列表（空 slice 表示无问题）
func ValidateApp(cfg *AppConfig) []string {
	var errs []string

	if cfg == nil {
		return []string{"配置为空"}
	}

	// Server
	if cfg.Server.Addr == "" {
		errs = append(errs, "server.addr 不能为空")
	}

	// Pipeline
	if cfg.Pipeline.BatchSize <= 0 {
		errs = append(errs, "pipeline.batch_size 必须 > 0")
	}
	if cfg.Pipeline.LingerInterval <= 0 {
		errs = append(errs, "pipeline.linger_interval 必须 > 0")
	}

	// Collector
	if cfg.Collector.Interval <= 0 {
		errs = append(errs, "collector.interval 必须 > 0")
	}
	if cfg.Collector.JitterRatio < 0 || cfg.Collector.JitterRatio > 1 {
		errs = append(errs, "collector.jitter_ratio 必须在 [0,1] 范围内")
	}
	if cfg.Collector.MaxAgeDays <= 0 {
		errs = append(errs, "collector.max_age_days 必须 > 0")
	}
	if len(cfg.Collector.Sources) == 0 {
		errs = append(errs, "collector.sources 至少配置一个源")
	}
	// 豆瓣源校验
	for _, src := range cfg.Collector.Sources {
		if src == "douban" && len(cfg.Collector.Douban.Groups) == 0 {
			errs = append(errs, "collector.douban.groups 不能为空（源 douban 已启用）")
		}
	}

	// Filter
	if cfg.Filter.AIEnabled == nil {
		errs = append(errs, "filter.ai_enabled 未设置（应为 bool）")
	}
	if cfg.Filter.AIBatchSize <= 0 {
		errs = append(errs, "filter.ai_batch_size 必须 > 0")
	}

	// Notifier
	if cfg.Notifier.MaxAttempts <= 0 {
		errs = append(errs, "notifier.max_attempts 必须 > 0")
	}
	if cfg.Notifier.RetryBaseInterval <= 0 {
		errs = append(errs, "notifier.retry_base_interval 必须 > 0")
	}

	return errs
}

// ValidateEnv 校验敏感配置，返回错误消息列表（空 slice 表示无问题）
func ValidateEnv(env *EnvLocalConfig) []string {
	var errs []string

	if env == nil {
		return []string{"敏感配置为空"}
	}

	// Collector 校验：所有源的 cookie 模式
	// douban
	dc := env.Collector.Douban
	mode := strings.ToLower(dc.CookieMode)
	switch mode {
	case "none":
		// 无需额外校验
	case "file":
		if dc.CookieFile == "" {
			errs = append(errs, "collector.douban.cookie_file 不能为空（cookie_mode=file）")
		}
	case "cookiecloud":
		if dc.CookiecloudURL == "" {
			errs = append(errs, "collector.douban.cookiecloud_url 不能为空（cookie_mode=cookiecloud）")
		}
		if dc.CookiecloudKey == "" {
			errs = append(errs, "collector.douban.cookiecloud_key 不能为空（cookie_mode=cookiecloud）")
		}
		if dc.CookiecloudPass == "" {
			errs = append(errs, "collector.douban.cookiecloud_password 不能为空（cookie_mode=cookiecloud）")
		}
	case "":
		errs = append(errs, "collector.douban.cookie_mode 未设置，请设置为 none/file/cookiecloud")
	default:
		errs = append(errs, "collector.douban.cookie_mode 必须是 none/file/cookiecloud")
	}

	// Filter: LLM 校验（api_key 空则自动关闭，不报错；base_url 非空则校验）
	llm := env.Filter.LLM
	if llm.APIKey == "" {
		// api_key 空 = AI 自动关闭，允许
	} else {
		if llm.BaseURL == "" {
			errs = append(errs, "filter.llm.base_url 不能为空（api_key 已配置）")
		}
		if llm.Model == "" {
			errs = append(errs, "filter.llm.model 不能为空（api_key 已配置）")
		}
	}

	// Notifier: 各渠道校验（有 webhook 则需至少有效 URL）
	n := env.Notifier
	if n.Feishu.Webhook != "" && !strings.HasPrefix(n.Feishu.Webhook, "http") {
		errs = append(errs, "notifier.feishu.webhook 必须为 http(s) URL")
	}
	if n.Dingtalk.Webhook != "" && !strings.HasPrefix(n.Dingtalk.Webhook, "http") {
		errs = append(errs, "notifier.dingtalk.webhook 必须为 http(s) URL")
	}
	if n.Wecom.Webhook != "" && !strings.HasPrefix(n.Wecom.Webhook, "http") {
		errs = append(errs, "notifier.wecom.webhook 必须为 http(s) URL")
	}
	if n.Pushplus.Token != "" {
		// 仅 token 长度检查（可留空）
		if len(n.Pushplus.Token) < 8 {
			errs = append(errs, "notifier.pushplus.token 长度至少 8 字符（如已配置）")
		}
	}
	if n.Serverchan.Sendkey != "" {
		if len(n.Serverchan.Sendkey) < 8 {
			errs = append(errs, "notifier.serverchan.sendkey 长度至少 8 字符（如已配置）")
		}
	}
	if n.Webhook.URL != "" && !strings.HasPrefix(n.Webhook.URL, "http") {
		errs = append(errs, "notifier.webhook.url 必须为 http(s) URL")
	}

	return errs
}
