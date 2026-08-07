package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

// EnvLocalConfig 敏感配置（config.env.local.toml，gitignore，不入库）
type EnvLocalConfig struct {
	Admin     AdminConfig     `toml:"admin"`     // 常规：鉴权
	Collector EnvCollector    `toml:"collector"` // 收集：每源 cookie
	Filter    EnvFilter       `toml:"filter"`    // 过滤：LLM
	Notifier  EnvNotifier     `toml:"notifier"`  // 通知：各渠道 webhook
}

// AdminConfig 管理面鉴权（规格 7.1）
type AdminConfig struct {
	AuthRequired bool   `toml:"auth_required"`
	Token        string `toml:"token"`
}

// EnvCollector 每源敏感配置；新增源在此扩展字段
type EnvCollector struct {
	Douban DoubanCookieConfig `toml:"douban"`
}

// DoubanCookieConfig 豆瓣 cookie 三种模式（规格 4.4）
type DoubanCookieConfig struct {
	CookieMode       string `toml:"cookie_mode"` // none / file / cookiecloud
	CookieFile       string `toml:"cookie_file"`
	CookiecloudURL   string `toml:"cookiecloud_url"`
	CookiecloudKey   string `toml:"cookiecloud_key"`
	CookiecloudPass  string `toml:"cookiecloud_password"`
}

// EnvFilter 过滤敏感配置
type EnvFilter struct {
	LLM LLMConfig `toml:"llm"` // 兼容 OpenAI 接口
}

// LLMConfig LLM 服务商配置；api_key 空 = AI 规则链自动关闭
type LLMConfig struct {
	APIKey         string   `toml:"api_key"`
	BaseURL        string   `toml:"base_url"`
	Model          string   `toml:"model"`
	FallbackModels []string `toml:"fallback_models"`
}

// EnvNotifier 各渠道 webhook；配了的渠道自动启用（规格 7.2 约定大于配置）
type EnvNotifier struct {
	Feishu    WebhookSecretConfig `toml:"feishu"`
	Dingtalk  DingtalkConfig      `toml:"dingtalk"`
	Wecom     WebhookSecretConfig `toml:"wecom"`
	Pushplus  PushplusConfig      `toml:"pushplus"`
	Serverchan ServerchanConfig   `toml:"serverchan"`
	Webhook   CustomWebhookConfig `toml:"webhook"`
}

// WebhookSecretConfig 普通 webhook 渠道
type WebhookSecretConfig struct {
	Webhook string `toml:"webhook"`
}

// DingtalkConfig 钉钉额外支持加签密钥
type DingtalkConfig struct {
	Webhook string `toml:"webhook"`
	Secret  string `toml:"secret"`
}

// PushplusConfig 微信推送
type PushplusConfig struct {
	Token string `toml:"token"`
}

// ServerchanConfig 微信推送
type ServerchanConfig struct {
	Sendkey string `toml:"sendkey"`
}

// CustomWebhookConfig 自定义 webhook（可选 JSON 模板）
type CustomWebhookConfig struct {
	URL      string `toml:"url"`
	Template string `toml:"template"`
}

// LoadEnvLocal 加载敏感配置；文件缺失 = 未配能力自动关闭，不算错误
func LoadEnvLocal(path string) (*EnvLocalConfig, error) {
	env := &EnvLocalConfig{}
	if path == "" {
		return env, nil
	}
	if _, err := toml.DecodeFile(path, env); err != nil {
		// 文件缺失不算错误（区别于公开配置 Load 的强校验）；其余错误如实上报
		if os.IsNotExist(err) {
			return env, nil
		}
		return nil, err
	}
	return env, nil
}
