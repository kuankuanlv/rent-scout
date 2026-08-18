package config

import "strings"

// DefaultKV 一键导入的完整基线配置（参考现网 sqlite 脱敏整理）。
// 规则：敏感项（cookie/cookiecloud/LLM key/webhook）一律空；鉴权键不包含；
// 仅覆盖同名键，不影响用户已填内容。
func DefaultKV() map[string]string {
	return map[string]string{
		// server / log
		"server.addr":        ":7777",
		"server.public_base": "",
		"log.level":          "info",
		"log.format":         "text",
		"log.path":           "",
		"log.memory_lines":   "1000",
		// collector
		"collector.sources":           "",
		"collector.interval":          "300",
		"collector.jitter_ratio":      "0.2",
		"collector.max_age_days":      "7",
		"collector.douban.groups":     strings.Join(defaultDoubanGroups, "\n"),
		"collector.douban.interval":   "3",
		"collector.douban.range_from": "-10",
		"collector.douban.range_to":   "now",
		"collector.weibo.supertopics": strings.Join([]string{
			"https://weibo.com/p/100808453110d9ea6a7b6fd15e79788cf55186/super_index",
			"https://weibo.com/p/1008086281f8282666f0277296947874553d4/super_index",
			"https://weibo.com/p/10080878830812400a0d2183389813e33621c/super_index",
			"https://weibo.com/p/100808f169281027a522764913769d0c34627/super_index",
			"https://weibo.com/p/10080891d418288a880181d26832436861052/super_index",
		}, "\n"),
		"collector.weibo.users": strings.Join([]string{
			"https://m.weibo.cn/u/6342026928",
			"https://m.weibo.cn/u/5203421220",
			"https://m.weibo.cn/u/7302156473",
			"https://m.weibo.cn/u/5574362274",
			"https://m.weibo.cn/u/7510434667",
			"https://m.weibo.cn/u/5167446030",
			"https://m.weibo.cn/u/7425336841",
			"https://m.weibo.cn/u/5783034097",
			"https://m.weibo.cn/u/7601729256",
			"https://m.weibo.cn/u/5947248123",
		}, "\n"),
		"collector.weibo.interval":   "5",
		"collector.weibo.range_from": "-10",
		// filter
		"filter.ai_enabled":    "true",
		"filter.batch_size":    "20",
		"filter.ai_batch_size": "10",
		// notifier（channels 留空，避免空 webhook 反复报错）
		"notifier.batch_size": "10",
		"notifier.interval":   "7200",
		"notifier.channels":   "",
		// secrets：cookie 全部 none / 空
		"secret.collector.douban.cookie_mode":          "none",
		"secret.collector.douban.cookie_raw":           "",
		"secret.collector.douban.cookiecloud_url":      "",
		"secret.collector.douban.cookiecloud_key":      "",
		"secret.collector.douban.cookiecloud_password": "",
		"secret.collector.weibo.cookie_mode":           "none",
		"secret.collector.weibo.cookie_raw":            "",
		"secret.collector.weibo.cookie_raw_cn":         "",
		"secret.collector.weibo.cookiecloud_url":       "",
		"secret.collector.weibo.cookiecloud_key":       "",
		"secret.collector.weibo.cookiecloud_password":  "",
		// secrets：LLM 默认 DeepSeek，key 空
		"secret.filter.llm.api_style":       "openai",
		"secret.filter.llm.api_key":         "",
		"secret.filter.llm.base_url":        DefaultLLMBaseURL,
		"secret.filter.llm.model":           DefaultLLMModel,
		"secret.filter.llm.fallback_models": "",
		// secrets：通知渠道全空
		"secret.notifier.feishu.webhook":     "",
		"secret.notifier.dingtalk.webhook":   "",
		"secret.notifier.dingtalk.secret":    "",
		"secret.notifier.wecom.webhook":      "",
		"secret.notifier.pushplus.token":     "",
		"secret.notifier.pushplus.topic":     "",
		"secret.notifier.serverchan.sendkey": "",
		"secret.notifier.webhook.url":        "",
		"secret.notifier.webhook.template":   "",
	}
}
