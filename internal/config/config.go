package config

// AppConfig 公开配置，按规格 7.2 四类组织
type AppConfig struct {
	Server    ServerConfig    // 常规：服务运行基础
	Log       LogConfig       // 常规：日志
	Pipeline  PipelineConfig  // 已废弃公开 UI；仅旧 KV 迁移 fallback
	Collector CollectorConfig // 收集
	Filter    FilterConfig    // 过滤
	Notifier  NotifierConfig  // 通知
	Admin     AdminConfig     // 管理：鉴权
}

// ServerConfig HTTP 监听
type ServerConfig struct {
	Addr string
}

// LogConfig 日志（path 空 = stdout，配了才写文件轮转）
type LogConfig struct {
	Level  string
	Format string
	Path   string // 可选：日志文件路径（空=stdout）
}

// PipelineConfig 旧组批字段（UI 已拆到 filter/notifier）；读旧 pipeline.batch_size 作 fallback
type PipelineConfig struct {
	BatchSize      int // 旧 key；新 filter/notifier.batch_size 空时回落
	LingerInterval int // 旧 key；linger 改用常量，不再校验/写 UI
}

// CollectorConfig 收集：源清单 + 全局频率 + 每源配置
type CollectorConfig struct {
	Sources     []string
	Interval    int          // 全局默认采集间隔（秒）
	JitterRatio float64      // 间隔随机抖动比例
	MaxAgeDays  int          // 时间窗：超过此天数的帖子不再采集（规格 4.5）
	Douban      DoubanConfig // 豆瓣源（新增源在此扩展）
}

// DoubanConfig 豆瓣源公开配置
type DoubanConfig struct {
	Groups    []string // 豆瓣租房小组 URL
	Interval  int      // 覆盖全局间隔（0 = 用全局）
	RangeFrom string   // 拉取范围起点；默认 -10d（相对 now）
	RangeTo   string   // 拉取范围终点；默认 now（每次运行时解析，禁止存死墙钟）
}

// FilterConfig 过滤：AI 开关与效率
type FilterConfig struct {
	AIEnabled   *bool // nil = 未配置，默认启用；显式 false 保留
	BatchSize   int   // pipeline 拉批大小（从库拉取上限）
	AIBatchSize int   // AI 子批上限（LLM 一次判定条数）
}

// NotifierConfig 通知：重试参数；渠道启用遵循约定（配了 webhook 自动启用）
type NotifierConfig struct {
	MaxAttempts       int
	RetryBaseInterval int
	BatchSize         int      // pipeline 拉批大小
	Channels          []string // 可选白名单；空 = 按已配 webhook 自动启用
}

// AdminConfig 管理面鉴权（规格 7.1；Docker 部署安全考虑）
type AdminConfig struct {
	AuthRequired bool   // 是否启用鉴权；false=默认不鉴权（启动 WARN 强提醒）
	Token        string // 访问口令；auth_required=true 且为空时启动随机生成
}

// Secrets 敏感配置（存 SQLite secret.* 前缀）
type Secrets struct {
	Collector SecretsCollector
	Filter    SecretsFilter
	Notifier  SecretsNotifier
}

// SecretsCollector 每源敏感配置
type SecretsCollector struct {
	Douban DoubanCookieConfig
}

// DoubanCookieConfig 豆瓣 cookie 三模式 none|raw|cookiecloud（规格 §5）
type DoubanCookieConfig struct {
	CookieMode      string
	CookieRaw       string
	CookieFile      string // 废弃：旧 file 模式 KV 兼容字段，读库时忽略
	CookiecloudURL  string
	CookiecloudKey  string
	CookiecloudPass string
}

// SecretsFilter 过滤敏感配置
type SecretsFilter struct {
	LLM LLMConfig
}

// LLMConfig LLM 服务商配置
type LLMConfig struct {
	APIKey         string
	BaseURL        string
	Model          string
	FallbackModels []string
		APIStyle       string // none | openai | other；旧 custom 读入当 other；默认 openai
	}

// SecretsNotifier 各渠道 webhook
type SecretsNotifier struct {
	Feishu     WebhookSecretConfig
	Dingtalk   DingtalkConfig
	Wecom      WebhookSecretConfig
	Pushplus   PushplusConfig
	Serverchan ServerchanConfig
	Webhook    CustomWebhookConfig
}

// WebhookSecretConfig 普通 webhook 渠道
type WebhookSecretConfig struct {
	Webhook string
}

// DingtalkConfig 钉钉加签
type DingtalkConfig struct {
	Webhook string
	Secret  string
}

// PushplusConfig 微信推送
type PushplusConfig struct {
	Token string
}

// ServerchanConfig Server酱
type ServerchanConfig struct {
	Sendkey string
}

// CustomWebhookConfig 自定义 webhook
type CustomWebhookConfig struct {
	URL      string
	Template string
}

// 内置默认豆瓣小组（迁移自现有系统，北京热门）
var defaultDoubanGroups = []string{
	"https://www.douban.com/group/35417/discussion",
	"https://www.douban.com/group/262626/discussion",
	"https://www.douban.com/group/232413/discussion",
	"https://www.douban.com/group/338147/discussion",
	"https://www.douban.com/group/331294/discussion",
	"https://www.douban.com/group/550436/discussion",
	"https://www.douban.com/group/596202/discussion",
}

// DefaultApp 内置默认公开配置（SQLite 空库时使用）
func DefaultApp() *AppConfig {
	cfg := &AppConfig{}
	applyDefaults(cfg)
	return cfg
}

// DefaultSecrets 内置默认敏感配置（全空 = 能力关闭）
func DefaultSecrets() *Secrets {
	return &Secrets{
		Collector: SecretsCollector{
			Douban: DoubanCookieConfig{CookieMode: "none"},
		},
	}
}

// applyDefaults 约定大于配置：缺省值对个人场景开箱即用（规格 7.2）
func applyDefaults(cfg *AppConfig) {
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = ":7777"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	if len(cfg.Collector.Sources) == 0 {
		cfg.Collector.Sources = []string{"douban"}
	}
	if cfg.Collector.Interval == 0 {
		cfg.Collector.Interval = 1800
	}
	if cfg.Collector.JitterRatio == 0 {
		cfg.Collector.JitterRatio = 0.2
	}
	if cfg.Collector.MaxAgeDays == 0 {
		cfg.Collector.MaxAgeDays = 7 // 非豆瓣源时间窗默认；豆瓣以 RangeFrom/To 为准
	}
	// 豆瓣默认启用内置小组；覆盖了 interval 才用覆盖值
	if len(cfg.Collector.Douban.Groups) == 0 {
		cfg.Collector.Douban.Groups = defaultDoubanGroups
	}
	if cfg.Collector.Douban.RangeFrom == "" {
		cfg.Collector.Douban.RangeFrom = "-10d"
	}
	if cfg.Collector.Douban.RangeTo == "" {
		cfg.Collector.Douban.RangeTo = "now"
	}
	// 组批：优先新 key；空则回落旧 pipeline.batch_size，再默认 20
	if cfg.Filter.BatchSize == 0 {
		if cfg.Pipeline.BatchSize > 0 {
			cfg.Filter.BatchSize = cfg.Pipeline.BatchSize
		} else {
			cfg.Filter.BatchSize = 20
		}
	}
	if cfg.Filter.AIBatchSize == 0 {
		cfg.Filter.AIBatchSize = 10
	}
	// AI 默认启用（配了 LLM key 才真正生效，未配自动跳过）；显式 false 保留
	if cfg.Filter.AIEnabled == nil {
		enabled := true
		cfg.Filter.AIEnabled = &enabled
	}
	if cfg.Notifier.BatchSize == 0 {
		if cfg.Pipeline.BatchSize > 0 {
			cfg.Notifier.BatchSize = cfg.Pipeline.BatchSize
		} else {
			cfg.Notifier.BatchSize = 20
		}
	}
	if cfg.Notifier.MaxAttempts == 0 {
		cfg.Notifier.MaxAttempts = 3
	}
	if cfg.Notifier.RetryBaseInterval == 0 {
		cfg.Notifier.RetryBaseInterval = 300
	}
}

// SourceInterval 某源的采集间隔：源配置了覆盖（interval>0）用之，否则全局默认（规格 4.5）
func (c CollectorConfig) SourceInterval(source string) int {
	if source == "douban" && c.Douban.Interval > 0 {
		return c.Douban.Interval
	}
	return c.Interval
}
