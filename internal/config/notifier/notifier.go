package notifier

type Config struct {
	BatchSize int
	Interval  int
	Channels  []string
}

type SecretsNotifier struct {
	Feishu     WebhookSecretConfig
	Dingtalk   DingtalkConfig
	Wecom      WebhookSecretConfig
	Pushplus   PushplusConfig
	Serverchan ServerchanConfig
	Webhook    CustomWebhookConfig
}

type WebhookSecretConfig struct {
	Webhook string
}

type DingtalkConfig struct {
	Webhook string
	Secret  string
}

type PushplusConfig struct {
	Token string
	Topic string
}

type ServerchanConfig struct {
	Sendkey string
}

type CustomWebhookConfig struct {
	URL      string
	Template string
}

const (
	DefaultNotifierBatch    = 10
	DefaultNotifierInterval = 7200
)

func ApplyDefaults(c *Config) {
	if c.BatchSize == 0 {
		c.BatchSize = DefaultNotifierBatch
	}
	if c.Interval == 0 {
		c.Interval = DefaultNotifierInterval
	}
}
