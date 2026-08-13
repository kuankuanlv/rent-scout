package config

import (
	"strings"
	"time"

	"rent-scout/internal/models"
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
	if cfg.Log.MemoryLines < MinLogMemoryLines || cfg.Log.MemoryLines > MaxLogMemoryLines {
		errs = append(errs, "log.memory_lines 须在 100–10000（占进程内存，探测日志更吃内存）")
	}

	// Collector
	if cfg.Collector.Interval <= 0 {
		errs = append(errs, "collector.interval 必须 > 0")
	}
	if cfg.Collector.Douban.Interval <= 0 {
		errs = append(errs, "collector.douban.interval 必须 > 0")
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
		if src == models.SourceDouban.String() && len(cfg.Collector.Douban.Groups) == 0 {
			errs = append(errs, "collector.douban.groups 不能为空（源 douban 已启用）")
		}
	}
	if _, _, err := ResolveTimeRange(cfg.Collector.Douban.RangeFrom, cfg.Collector.Douban.RangeTo, time.Now()); err != nil {
		errs = append(errs, "collector.douban 拉取范围: "+err.Error())
	}

	// Filter
	if cfg.Filter.AIEnabled == nil {
		errs = append(errs, "filter.ai_enabled 未设置（应为 bool）")
	}
	if cfg.Filter.BatchSize <= 0 {
		errs = append(errs, "filter.batch_size 必须 > 0")
	}
	if cfg.Filter.AIBatchSize <= 0 {
		errs = append(errs, "filter.ai_batch_size 必须 > 0")
	}

	// Notifier
	if cfg.Notifier.BatchSize <= 0 {
		errs = append(errs, "notifier.batch_size 必须 > 0")
	}
	if cfg.Notifier.MaxAttempts <= 0 {
		errs = append(errs, "notifier.max_attempts 必须 > 0")
	}
	if cfg.Notifier.RetryBaseInterval <= 0 {
		errs = append(errs, "notifier.retry_base_interval 必须 > 0")
	}

	return errs
}

// ValidateSecrets 校验敏感配置，返回错误消息列表（空 slice 表示无问题）
func ValidateSecrets(sec *Secrets) []string {
	var errs []string

	if sec == nil {
		return []string{"敏感配置为空"}
	}

	// Collector 校验：所有源的 cookie 模式
	// douban
	dc := sec.Collector.Douban
	mode := ParseCookieMode(dc.CookieMode)
	switch mode {
	case CookieModeNone:
		// 空串与 none 同等，不要求 raw/cloud 字段
	case CookieModeRaw:
		// 保存路径：空提交已沿用已存 cookie_raw，此处看到的应是合并后的值
		if strings.TrimSpace(dc.CookieRaw) == "" {
			errs = append(errs, "collector.douban.cookie_raw 不能为空（cookie_mode=raw）")
		}
	case CookieModeCookieCloud:
		if dc.CookiecloudURL == "" {
			errs = append(errs, "collector.douban.cookiecloud_url 不能为空（cookie_mode=cookiecloud）")
		}
		if dc.CookiecloudKey == "" {
			errs = append(errs, "collector.douban.cookiecloud_key 不能为空（cookie_mode=cookiecloud）")
		}
		if dc.CookiecloudPass == "" {
			errs = append(errs, "collector.douban.cookiecloud_password 不能为空（cookie_mode=cookiecloud）")
		}
	default:
		if strings.EqualFold(dc.CookieMode, "file") {
			errs = append(errs, "collector.douban.cookie_mode=file 已移除，请改用 none/raw/cookiecloud")
		} else {
			errs = append(errs, "collector.douban.cookie_mode 必须是 none/raw/cookiecloud")
		}
	}

	// Filter: LLM 校验（api_key 空则自动关闭，不报错；base_url 非空则校验）
	llm := sec.Filter.LLM
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
	n := sec.Notifier
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
