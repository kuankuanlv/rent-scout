package config

// EnvLocalConfig 敏感配置（存 SQLite secret.* 前缀）
type EnvLocalConfig struct {
	Collector EnvCollector `toml:"collector"`
	Filter    EnvFilter    `toml:"filter"`
	Notifier  EnvNotifier  `toml:"notifier"`
}

// EnvCollector 每源敏感配置
type EnvCollector struct {
	Douban DoubanCookieConfig `toml:"douban"`
}

// DoubanCookieConfig 豆瓣 cookie 四模式 none|raw|file|cookiecloud（规格 §5）
type DoubanCookieConfig struct {
	CookieMode      string `toml:"cookie_mode"`
	CookieRaw       string `toml:"cookie_raw"`
	CookieFile      string `toml:"cookie_file"`
	CookiecloudURL  string `toml:"cookiecloud_url"`
	CookiecloudKey  string `toml:"cookiecloud_key"`
	CookiecloudPass string `toml:"cookiecloud_password"`
}

// EnvFilter 过滤敏感配置
type EnvFilter struct {
	LLM LLMConfig `toml:"llm"`
}

// LLMConfig LLM 服务商配置
type LLMConfig struct {
	APIKey         string   `toml:"api_key"`
	BaseURL        string   `toml:"base_url"`
	Model          string   `toml:"model"`
	FallbackModels []string `toml:"fallback_models"`
}

// EnvNotifier 各渠道 webhook
type EnvNotifier struct {
	Feishu     WebhookSecretConfig `toml:"feishu"`
	Dingtalk   DingtalkConfig      `toml:"dingtalk"`
	Wecom      WebhookSecretConfig `toml:"wecom"`
	Pushplus   PushplusConfig      `toml:"pushplus"`
	Serverchan ServerchanConfig    `toml:"serverchan"`
	Webhook    CustomWebhookConfig `toml:"webhook"`
}

// WebhookSecretConfig 普通 webhook 渠道
type WebhookSecretConfig struct {
	Webhook string `toml:"webhook"`
}

// DingtalkConfig 钉钉加签
type DingtalkConfig struct {
	Webhook string `toml:"webhook"`
	Secret  string `toml:"secret"`
}

// PushplusConfig 微信推送
type PushplusConfig struct {
	Token string `toml:"token"`
}

// ServerchanConfig Server酱
type ServerchanConfig struct {
	Sendkey string `toml:"sendkey"`
}

// CustomWebhookConfig 自定义 webhook
type CustomWebhookConfig struct {
	URL      string `toml:"url"`
	Template string `toml:"template"`
}
