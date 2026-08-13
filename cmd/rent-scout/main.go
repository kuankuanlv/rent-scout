package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rent-scout/internal/admin"
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

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "db/rent-scout.db"
	}

	boot := pkglog.Component(pkglog.Main)

	db, err := store.Open(dbPath)
	if err != nil {
		boot.Error("[boot_db_failed] 打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	cnt, err := store.ConfigCount(db)
	if err != nil {
		boot.Error("[boot_config_count_failed] 读取配置条数失败", "err", err)
		os.Exit(1)
	}
	if cnt == 0 {
		boot.Warn("[boot_config_empty] 配置为空，请完成引导", "hint", "visit /admin/setup")
	}

	rt := config.NewRuntime(db)
	if err := rt.ReloadOnce(); err != nil {
		boot.Error("[boot_config_load_failed] 首次加载配置失败", "err", err)
		os.Exit(1)
	}
	cfg := rt.Get()

	logger := pkglog.New(cfg.Log)
	boot = logger.With("component", pkglog.Main)
	boot.Info("[boot_start] 服务启动",
		"addr", cfg.Server.Addr,
		"sources", cfg.Collector.Sources,
		"config_keys", cnt,
		"setup_done", store.IsSetupComplete(db),
	)

	// 鉴权 token：DB 为空则随机生成并写入
	adminToken := cfg.Admin.Token
	if cfg.Admin.AuthRequired && adminToken == "" {
		adminToken = randomHex(16)
		// 禁止日志打完整 token；仅记长度
		boot.Warn("[boot_admin_token_generated] 已生成管理员 Token", "token_len", len(adminToken))
		_ = store.SetConfig(db, "admin.token", adminToken)
		_ = rt.ReloadOnce()
	}
	if !cfg.Admin.AuthRequired {
		boot.Warn("[boot_auth_disabled] 鉴权已关闭")
	}

	stopReload := rt.WatchDB(10 * time.Second)
	defer stopReload()

	trigger := make(chan struct{}, 64)
	notifyTrigger := make(chan struct{}, 64)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := newCollectorRunner(rt, db, trigger)
	if runner != nil {
		go runner.Run(ctx)
	}

	fc := newFilterConsumer(rt, db, notifyTrigger)
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

	if nc := newNotifierConsumer(rt, db, notifyTrigger); nc != nil {
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

	srv := admin.NewServer(db, rt, runner)
	httpSrv := &http.Server{Addr: cfg.Server.Addr, Handler: srv.Handler()}
	go func() {
		boot.Info("[boot_http_listen] HTTP 开始监听", "addr", cfg.Server.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			boot.Error("[boot_http_failed] HTTP 服务失败", "err", err)
		}
	}()

	<-ctx.Done()
	boot.Info("[boot_shutdown] 正在关闭")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		boot.Warn("[boot_shutdown_timeout] 关闭超时", "err", err)
	}
}

// newCollectorRunner 从 Runtime 读配置与敏感项构造采集调度器
func newCollectorRunner(rt *config.Runtime, db *store.Store, trigger chan<- struct{}) *collector.Runner {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	var sources []collector.Source
	for _, name := range cfg.Collector.Sources {
		switch name {
		case "douban":
			// Cookie 每次 Get 从 Runtime 读最新配置，改 KV 后无需重启
			cookie := collector.NewRuntimeCookieProvider(rt)
			d, err := collector.NewDouban(collector.DoubanOptions{
				GroupURLs: cfg.Collector.Douban.Groups,
				Cookie:    cookie,
			})
			if err != nil {
				log.Error("[boot_source_init_failed] 源初始化失败", "source", name, "err", err)
				continue
			}
			sources = append(sources, d)
		default:
			log.Warn("[boot_source_unknown] 未知采集源", "source", name)
		}
	}
	if len(sources) == 0 {
		log.Warn("[boot_collector_skipped] 采集未启动")
		return nil
	}
	return collector.NewRunner(rt, db, sources, trigger)
}

func newFilterConsumer(rt *config.Runtime, db *store.Store, notifyTrigger chan<- struct{}) *pipeline.Consumer[models.RentPost] {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	env := rt.GetEnv()
	var ai filter.AIEvaluator
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
		log.Info("[boot_filter_ai_enabled] 筛选 AI 已启用", "model", model)
	} else {
		log.Warn("[boot_filter_ai_disabled] 筛选 AI 已关闭")
	}
	chain := filter.NewRuleChain(ai)
	fc := filter.NewConsumerWithOptions(chain, db, notifyTrigger, trimLimitFor(cfg, "douban"),
		filter.ConsumerOptions{AIBatchSize: cfg.Filter.AIBatchSize})
	return pipeline.New(
		fc.FetchBatch,
		func(ctx context.Context, batch []models.RentPost) error { return fc.ProcessBatch(ctx, batch) },
		pipeline.Options{
			BatchSize: cfg.Pipeline.BatchSize,
			Linger:    time.Duration(cfg.Pipeline.LingerInterval) * time.Second,
			Component: pkglog.Filter,
		},
	)
}

func newNotifierConsumer(rt *config.Runtime, db *store.Store, notifyTrigger <-chan struct{}) *pipeline.Consumer[models.RentPost] {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	env := rt.GetEnv()
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
		log.Warn("[boot_notifier_skipped] 通知未启动")
		return nil
	}
	// 反馈签名密钥每次从 Runtime 读，不在此钉死
	n := notifier.NewNotifier(db,
		notifier.NotifierOptions{MaxAttempts: cfg.Notifier.MaxAttempts, RetryBaseInterval: cfg.Notifier.RetryBaseInterval, Runtime: rt},
		channels...)
	return pipeline.New(
		func(ctx context.Context, limit int) ([]models.RentPost, error) {
			return db.FetchNotifyBatch(channelNames(channels), limit)
		},
		n.ProcessBatch,
		pipeline.Options{
			BatchSize: cfg.Pipeline.BatchSize,
			Linger:    time.Duration(cfg.Pipeline.LingerInterval) * time.Second,
			Component: pkglog.Notifier,
		},
	)
}

func channelNames(channels []notifier.Channel) []string {
	names := make([]string, len(channels))
	for i, c := range channels {
		names[i] = c.Name()
	}
	return names
}

func trimLimitFor(cfg *config.AppConfig, source string) int {
	if cfg.Filter.TrimLimits != nil {
		if n, ok := cfg.Filter.TrimLimits[source]; ok && n > 0 {
			return n
		}
	}
	return 500
}

func trimLimits(cfg *config.AppConfig) map[string]int {
	limits := map[string]int{}
	for src, n := range cfg.Filter.TrimLimits {
		if n > 0 {
			limits[src] = n
		}
	}
	return limits
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
