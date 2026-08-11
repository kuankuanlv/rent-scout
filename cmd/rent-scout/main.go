package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/filter"
	"rent-scout/internal/filter/llm"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier"
	"rent-scout/internal/pipeline"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// 默认配置文件路径与数据库路径（约定，db/ 目录 gitignore）
const (
	pubConfigPath = "config.toml"
	envConfigPath = "config.env.local.toml"
	dbPath        = "db/rent-scout.db"
)

func main() {
	// 加载配置：使用者入口文件必须存在（规格 7.2）
	cfg, err := config.Load(pubConfigPath)
	if err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}
	logger := pkglog.New(cfg.Log)
	rt := config.NewRuntime(cfg)
	logger.Info("rent-scout 启动", "addr", cfg.Server.Addr, "sources", cfg.Collector.Sources)

	// 打开数据库：建表幂等，重复启动安全
	db, err := store.Open(dbPath)
	if err != nil {
		logger.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("数据库就绪", "path", dbPath)

	// 敏感配置：启动即加载（collector cookie / notifier webhook 需要；缺失静默）
	envCfg, err := config.LoadEnvLocal(envConfigPath)
	if err != nil {
		logger.Error("加载敏感配置失败", "err", err)
		os.Exit(1)
	}

	// 配置热加载（Runtime 并发安全快照）
	stopReload := rt.Watch(pubConfigPath, envConfigPath, 10_000_000_000, nil)
	defer stopReload()

	// 下游（filter）触发通道：collector 写信号（计划 2 已有）
	trigger := make(chan struct{}, 64)
	// filter → notifier 触发通道（计划 4 notifier 消费）
	notifyTrigger := make(chan struct{}, 64)

	// 启动采集模块：每源独立 goroutine（规格 4.5）。
	// 信号 ctx 直达 runner：SIGINT/SIGTERM 到达即取消（信号取消即停，最简接线）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := newCollectorRunner(rt, envCfg, db, trigger)
	if runner != nil {
		go runner.Run(ctx)
	}

	// 启动筛选模块（规格 5.x）：消费 collector 信号 + linger 兜底。
	// 桥接 goroutine：collector 的 trigger 信号 → pipeline 内部 trigger（Signal 非阻塞，满则丢靠 linger）
	fc := newFilterConsumer(rt, envCfg, db, notifyTrigger)
	if fc != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-trigger:
					fc.Signal()
				}
			}
		}()
		go fc.Run(ctx)
	}

	// notifier 消费 notifyTrigger（passed 帖子信号，计划 4）
	if nc := newNotifierConsumer(rt, envCfg, db, notifyTrigger); nc != nil {
		// 桥接 goroutine：filter 的 notifyTrigger 信号 → pipeline 内部 trigger（Signal 非阻塞，满则丢靠 linger 兜底）
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-notifyTrigger:
					nc.Signal()
				}
			}
		}()
		go nc.Run(ctx)
	}

	// 优雅退出：等待信号后收尾（模块 goroutine 已随 ctx 取消停止）
	<-ctx.Done()
	logger.Info("收到退出信号，正在关闭")
}

// newCollectorRunner 按配置构造采集调度器；无可用源时返回 nil（不启动采集）
func newCollectorRunner(rt *config.Runtime, env *config.EnvLocalConfig, db *store.Store, trigger chan<- struct{}) *collector.Runner {
	var sources []collector.Source
	for _, name := range rt.Get().Collector.Sources {
		switch name {
		case "douban":
			// cookie provider：按 envlocal cookie_mode 选择（规格 4.4）
			dc := env.Collector.Douban
			cookie, err := collector.NewCookieProvider(dc.CookieMode, dc.CookieFile, dc)
			if err != nil {
				slog.Error("douban cookie provider 初始化失败，使用匿名", "err", err)
				cookie, _ = collector.NewCookieProvider("none", "", dc)
			}
			d, err := collector.NewDouban(collector.DoubanOptions{
				GroupURLs: rt.Get().Collector.Douban.Groups,
				Cookie:    cookie,
			})
			if err != nil {
				slog.Error("douban 适配器初始化失败", "err", err)
				continue
			}
			sources = append(sources, d)
		default:
			slog.Warn("未知源，跳过", "source", name)
		}
	}
	if len(sources) == 0 {
		slog.Warn("没有可用的源，采集模块不启动")
		return nil
	}
	return collector.NewRunner(rt, env, db, sources, trigger)
}

// newFilterConsumer 按配置构造筛选消费器；AI 未配置/未启用时只走硬编码链。
// collector 的 trigger 信号由 main 的桥接 goroutine 消费（此处无需再传）。
// notifyTrigger：passed 帖子信号（计划 4）
func newFilterConsumer(rt *config.Runtime, env *config.EnvLocalConfig, db *store.Store,
	notifyTrigger chan<- struct{}) *pipeline.Consumer[models.RentPost] {

	cfg := rt.Get()
	// AI 链：ai_enabled 默认 true；未配 LLM key → 自动跳过 AI（WARN，规格 7.2 约定）
	var ai filter.AIEvaluator
	// 注意：applyDefaults 保证 AIEnabled 非 nil（nil 语义"未配置=默认启用"已被遮蔽）；此守卫仅为防御
	if cfg.Filter.AIEnabled != nil && *cfg.Filter.AIEnabled && env.Filter.LLM.APIKey != "" {
		baseURL := env.Filter.LLM.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		model := env.Filter.LLM.Model
		if model == "" {
			model = "deepseek-chat"
		}
		opts := []llm.ClientOptions{{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: model}}
		for _, m := range env.Filter.LLM.FallbackModels {
			opts = append(opts, llm.ClientOptions{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: m})
		}
		pool := llm.NewPool(opts, llm.PoolOptions{})
		ai = filter.NewAIBatchEvaluator(pool, trimLimits(cfg))
		slog.Info("AI 筛选已启用", "model", model, "fallbacks", env.Filter.LLM.FallbackModels)
	} else {
		slog.Warn("AI 筛选未启用（未配 LLM key 或已关闭），只走硬编码规则")
	}
	chain := filter.NewRuleChain(ai)
	fc := filter.NewConsumerWithOptions(chain, db, notifyTrigger, trimLimitFor(cfg, "douban"),
		filter.ConsumerOptions{AIBatchSize: cfg.Filter.AIBatchSize})
	// pipeline 协议：trigger 信号 + linger 兜底（规格 2.3）
	return pipeline.New(
		fc.FetchBatch,
		func(ctx context.Context, batch []models.RentPost) error { return fc.ProcessBatch(ctx, batch) },
		pipeline.Options{BatchSize: cfg.Pipeline.BatchSize, Linger: time.Duration(cfg.Pipeline.LingerInterval) * time.Second},
	)
}

// newNotifierConsumer 按配置构造通知消费器（规格 6.5）：
// 拉批 passed 未发送帖 → 分组 → 各启用渠道发送；失败重试/死信（规格 6.6）
func newNotifierConsumer(rt *config.Runtime, env *config.EnvLocalConfig, db *store.Store,
	notifyTrigger <-chan struct{}) *pipeline.Consumer[models.RentPost] {

	cfg := rt.Get()
	// 启用渠道：显式 channels 白名单 > 已配 webhook 自动启用（规格 7.2 约定大于配置）
	var channels []notifier.Channel
	enabled := notifier.EnabledChannels(env.Notifier)
	if len(cfg.Notifier.Channels) > 0 {
		enabled = cfg.Notifier.Channels
	}
	for _, name := range enabled {
		switch name {
		case notifier.ChannelFeishu:
			if env.Notifier.Feishu.Webhook != "" {
				channels = append(channels, notifier.NewFeishuChannel(env.Notifier.Feishu.Webhook))
			}
		case notifier.ChannelDingtalk:
			if env.Notifier.Dingtalk.Webhook != "" {
				channels = append(channels, notifier.NewDingtalkChannel(env.Notifier.Dingtalk.Webhook, env.Notifier.Dingtalk.Secret))
			}
		case notifier.ChannelWecom:
			if env.Notifier.Wecom.Webhook != "" {
				channels = append(channels, notifier.NewWecomChannel(env.Notifier.Wecom.Webhook))
			}
		case notifier.ChannelPushplus:
			if env.Notifier.Pushplus.Token != "" {
				channels = append(channels, notifier.NewPushplusChannel("", env.Notifier.Pushplus.Token))
			}
		case notifier.ChannelServerchan:
			if env.Notifier.Serverchan.Sendkey != "" {
				channels = append(channels, notifier.NewServerchanChannel(env.Notifier.Serverchan.Sendkey))
			}
		case notifier.ChannelWebhook:
			if env.Notifier.Webhook.URL != "" {
				channels = append(channels, notifier.NewWebhookChannel(env.Notifier.Webhook.URL, env.Notifier.Webhook.Template))
			}
		}
	}
	if len(channels) == 0 {
		slog.Warn("通知渠道未启用（env.local 未配置或白名单无匹配渠道），通知停用")
		return nil
	}
	// 重试参数：max_attempts/retry_base_interval（默认 3/300 由 applyDefaults 保证）
	n := notifier.NewNotifier(db,
		notifier.NotifierOptions{MaxAttempts: cfg.Notifier.MaxAttempts, RetryBaseInterval: cfg.Notifier.RetryBaseInterval},
		channels...)
	return pipeline.New(
		func(ctx context.Context, limit int) ([]models.RentPost, error) {
			return db.FetchNotifyBatch(channelNames(channels), limit)
		},
		n.ProcessBatch,
		pipeline.Options{BatchSize: cfg.Pipeline.BatchSize, Linger: time.Duration(cfg.Pipeline.LingerInterval) * time.Second},
	)
}

// channelNames 提取渠道名列表
func channelNames(channels []notifier.Channel) []string {
	names := make([]string, len(channels))
	for i, c := range channels {
		names[i] = c.Name()
	}
	return names
}

// trimLimitFor 某源 LLM 输入截断字数（trim_limits 配置，缺省 500——规格 7.2 注释语义）
func trimLimitFor(cfg *config.AppConfig, source string) int {
	if cfg.Filter.TrimLimits != nil {
		if n, ok := cfg.Filter.TrimLimits[source]; ok && n > 0 {
			return n
		}
	}
	return 500
}

// trimLimits 每源 LLM 输入截断字数映射（trim_limits 配置，规格 5.2）。
// 未配置的源由评估器按缺省 500 处理；>0 的配置才生效
func trimLimits(cfg *config.AppConfig) map[string]int {
	limits := map[string]int{}
	for src, n := range cfg.Filter.TrimLimits {
		if n > 0 {
			limits[src] = n
		}
	}
	return limits
}
