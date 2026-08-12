package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// AppConfig 公开配置（config.toml），按规格 7.2 四类组织
type AppConfig struct {
	Server    ServerConfig    `toml:"server"`    // 常规：服务运行基础
	Log       LogConfig       `toml:"log"`       // 常规：日志
	Pipeline  PipelineConfig  `toml:"pipeline"`  // 常规：组批协议
	Collector CollectorConfig `toml:"collector"` // 收集
	Filter    FilterConfig    `toml:"filter"`    // 过滤
	Notifier  NotifierConfig  `toml:"notifier"`  // 通知
	Admin     AdminConfig     `toml:"admin"`     // 管理：鉴权
}

// ServerConfig HTTP 监听
type ServerConfig struct {
	Addr string `toml:"addr"`
}

// LogConfig 日志（path 空 = stdout，配了才写文件轮转）
type LogConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
	Path   string `toml:"path"` // 可选：日志文件路径（空=stdout）
}

// PipelineConfig 模块间组批协议（filter/notifier 消费通用）
type PipelineConfig struct {
	BatchSize      int `toml:"batch_size"`
	LingerInterval int `toml:"linger_interval"`
}

// CollectorConfig 收集：源清单 + 全局频率 + 每源配置
type CollectorConfig struct {
	Sources     []string     `toml:"sources"`
	Interval    int          `toml:"interval"`     // 全局默认采集间隔（秒）
	JitterRatio float64      `toml:"jitter_ratio"` // 间隔随机抖动比例
	MaxAgeDays  int          `toml:"max_age_days"` // 时间窗：超过此天数的帖子不再采集（规格 4.5）
	Douban      DoubanConfig `toml:"douban"`       // 豆瓣源（新增源在此扩展）
}

// DoubanConfig 豆瓣源公开配置
type DoubanConfig struct {
	Groups   []string `toml:"groups"`   // 豆瓣租房小组 URL
	Interval int      `toml:"interval"` // 覆盖全局间隔（0 = 用全局）
}

// FilterConfig 过滤：AI 开关与效率
type FilterConfig struct {
	AIEnabled   *bool          `toml:"ai_enabled"` // nil = 未配置，默认启用；显式 false 保留
	AIBatchSize int            `toml:"ai_batch_size"`
	TrimLimits  map[string]int `toml:"trim_limits"` // 每源 LLM 输入截断字数
}

// NotifierConfig 通知：重试参数；渠道启用遵循约定（配了 webhook 自动启用）
type NotifierConfig struct {
	MaxAttempts       int      `toml:"max_attempts"`
	RetryBaseInterval int      `toml:"retry_base_interval"`
	Channels          []string `toml:"channels"` // 可选白名单；空 = 按已配 webhook 自动启用
}

// AdminConfig 管理面鉴权（规格 7.1；Docker 部署安全考虑）
type AdminConfig struct {
	AuthRequired bool   `toml:"auth_required"` // 是否启用鉴权；false=默认不鉴权（启动 WARN 强提醒）
	Token        string `toml:"token"`         // 访问口令；auth_required=true 且为空时启动随机生成
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
	if cfg.Pipeline.BatchSize == 0 {
		cfg.Pipeline.BatchSize = 20
	}
	if cfg.Pipeline.LingerInterval == 0 {
		cfg.Pipeline.LingerInterval = 120
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
		cfg.Collector.MaxAgeDays = 7 // 时间窗默认 7 天（0 会过滤掉全部帖子）
	}
	// 豆瓣默认启用内置小组；覆盖了 interval 才用覆盖值
	if len(cfg.Collector.Douban.Groups) == 0 {
		cfg.Collector.Douban.Groups = defaultDoubanGroups
	}
	if cfg.Filter.AIBatchSize == 0 {
		cfg.Filter.AIBatchSize = 10
	}
	// AI 默认启用（配了 LLM key 才真正生效，未配自动跳过）；显式 false 保留
	if cfg.Filter.AIEnabled == nil {
		enabled := true
		cfg.Filter.AIEnabled = &enabled
	}
	if cfg.Filter.TrimLimits == nil {
		cfg.Filter.TrimLimits = map[string]int{"douban": 800}
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

// Load 加载公开配置；envPath 为空则跳过敏感配置
func Load(path string) (*AppConfig, error) {
	cfg := &AppConfig{}
	// 文件必须存在（使用者入口）；meta 记录文件名供热加载用
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("配置文件缺失 %s: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	applyDefaults(cfg)
	return cfg, nil
}
