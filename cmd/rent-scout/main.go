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
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/collector/sources/douban"
	"rent-scout/internal/config"
	"rent-scout/internal/filter"
	"rent-scout/internal/filter/llm"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier"
	"rent-scout/internal/notifier/channels"
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
		boot.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.EnsureDefaultRule(); err != nil {
		boot.Error("写入默认规则失败", "err", err)
		os.Exit(1)
	}

	cnt, err := store.ConfigCount(db)
	if err != nil {
		boot.Error("读取配置条数失败", "err", err)
		os.Exit(1)
	}
	if cnt == 0 {
		boot.Warn("配置为空，请完成引导", "hint", "visit /admin/setup")
	}

	rt := config.NewHotConfig(db)
	rt.SetAfterReload(func(app *config.AppConfig) {
		if app != nil {
			pkglog.SetHubCap(app.Log.MemoryLines)
		}
	})
	if err := rt.ReloadOnce(); err != nil {
		boot.Error("首次加载配置失败", "err", err)
		os.Exit(1)
	}
	cfg := rt.Get()

	_ = pkglog.New(cfg.Log)
	boot = pkglog.Component(pkglog.Main)
	boot.Info("服务启动",
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
		boot.Warn("已生成管理员 Token", "token_len", len(adminToken))
		_ = store.SetConfig(db, "admin.token", adminToken)
		_ = rt.ReloadOnce()
	}
	if !cfg.Admin.AuthRequired {
		boot.Warn("鉴权已关闭")
	}

	stopReload := rt.WatchDB(10 * time.Second)
	defer stopReload()

	trigger := make(chan struct{}, 64)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runner := newCollectorRunner(rt, db, trigger)
	if runner != nil {
		go runner.Run(ctx)
	}
	go cookie.NewSyncer(rt, db, cookie.DefaultSyncInterval).Run(ctx)

	filterPipe, aiPipe, filterC := newFilterPipelines(rt, db)
	if filterPipe != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-trigger:
					filterPipe.Signal()
				}
			}
		}()
		go filterPipe.Run(ctx)
	}
	if aiPipe != nil {
		go aiPipe.Run(ctx)
	}

	if nc := newNotifierConsumer(rt, db); nc != nil {
		go nc.Run(ctx)
	}

	srv := admin.NewServer(db, rt, runner)
	replayCh := make(chan struct{}, 1)
	srv.SetOnRulesChanged(func() {
		select {
		case replayCh <- struct{}{}:
		default:
		}
	})
	go runRuleReplay(ctx, rt, db, filterC, replayCh)
	httpSrv := &http.Server{Addr: cfg.Server.Addr, Handler: srv.Handler()}
	go func() {
		boot.Info("HTTP 开始监听", "addr", cfg.Server.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			boot.Error("HTTP 服务失败", "err", err)
		}
	}()

	<-ctx.Done()
	boot.Info("正在关闭")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		boot.Warn("关闭超时", "err", err)
	}
}

// newCollectorRunner 从 HotConfig 读配置与敏感项构造采集调度器
func newCollectorRunner(rt *config.HotConfig, db *store.Store, trigger chan<- struct{}) *collector.Runner {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	var sources []collector.Source
	for _, name := range cfg.Collector.Sources {
		src, ok := models.ParseSource(name)
		if !ok {
			log.Warn("未知采集源", "source", name)
			continue
		}
		switch src {
		case models.SourceDouban:
			// Cookie 每次 Get 只读本地 cookie_raw，不打 CookieCloud
			cp := cookie.NewHotConfigProvider(rt)
			d, err := douban.NewDouban(douban.DoubanOptions{
				GroupURLs: cfg.Collector.Douban.Groups,
				Cookie:    cp,
			})
			if err != nil {
				log.Error("源初始化失败", "source", name, "err", err)
				continue
			}
			sources = append(sources, d)
		default:
			log.Warn("未知采集源", "source", name)
		}
	}
	if len(sources) == 0 {
		log.Warn("采集未启动")
		return nil
	}
	return collector.NewRunner(rt, db, sources, trigger)
}

func newFilterPipelines(rt *config.HotConfig, db *store.Store) (
	*pipeline.Consumer[models.RentPost], *pipeline.Consumer[models.RentPost], *filter.Consumer,
) {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	env := rt.Secrets()
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
		ai = filter.NewAIBatchEvaluator(pool, nil)
		log.Info("筛选 AI 已启用", "model", model)
	} else {
		log.Warn("筛选 AI 已关闭")
	}
	chain := filter.NewRuleChain(ai)
	fc := filter.NewConsumerWithOptions(chain, db, filter.ConsumerOptions{AIBatchSize: cfg.Filter.AIBatchSize})
	filterPipe := pipeline.New(
		fc.FetchCollected,
		fc.ProcessHard,
		pipeline.Options{
			BatchSize: cfg.Filter.BatchSize,
			Linger:    pipeline.DefaultLinger,
			Component: pkglog.Filter,
		},
	)
	aiPipe := pipeline.New(
		fc.FetchAwaitingAI,
		fc.ProcessAI,
		pipeline.Options{
			BatchSize: cfg.Filter.AIBatchSize,
			Linger:    pipeline.DefaultLinger,
			Component: pkglog.AIReview,
			WaitFull:  true,
		},
	)
	return filterPipe, aiPipe, fc
}

func runRuleReplay(ctx context.Context, rt *config.HotConfig, db *store.Store, c *filter.Consumer, ch <-chan struct{}) {
	log := pkglog.Component(pkglog.Filter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if c == nil {
				continue
			}
			cfg := rt.Get()
			now := time.Now()
			start, end, err := config.ResolveTimeRange(cfg.Collector.Douban.RangeFrom, "now", now)
			if err != nil {
				log.Warn("规则 replay 时间窗无效", "err", err)
				continue
			}
			posts, err := db.ListPublishedBetween(start, end, 2000)
			if err != nil {
				log.Error("规则 replay 拉帖失败", "err", err)
				continue
			}
			if err := c.ReplayHard(ctx, posts); err != nil {
				log.Error("规则 replay 失败", "err", err)
			}
		}
	}
}

func newNotifierConsumer(rt *config.HotConfig, db *store.Store) *pipeline.Consumer[models.RentPost] {
	log := pkglog.Component(pkglog.Main)
	cfg := rt.Get()
	env := rt.Secrets()
	var chs []notifier.Channel
	enabled := cfg.Notifier.Channels
	for _, name := range enabled {
		switch name {
		case notifier.ChannelFeishu:
			if env.Notifier.Feishu.Webhook != "" {
				chs = append(chs, channels.NewFeishuChannel(env.Notifier.Feishu.Webhook))
			}
		case notifier.ChannelDingtalk:
			if env.Notifier.Dingtalk.Webhook != "" {
				chs = append(chs, channels.NewDingtalkChannel(env.Notifier.Dingtalk.Webhook, env.Notifier.Dingtalk.Secret))
			}
		case notifier.ChannelWecom:
			if env.Notifier.Wecom.Webhook != "" {
				chs = append(chs, channels.NewWecomChannel(env.Notifier.Wecom.Webhook))
			}
		case notifier.ChannelPushplus:
			if env.Notifier.Pushplus.Token != "" {
				chs = append(chs, channels.NewPushplusChannel("", env.Notifier.Pushplus.Token, env.Notifier.Pushplus.Topic))
			}
		case notifier.ChannelServerchan:
			if env.Notifier.Serverchan.Sendkey != "" {
				chs = append(chs, channels.NewServerchanChannel(env.Notifier.Serverchan.Sendkey))
			}
		case notifier.ChannelWebhook:
			if env.Notifier.Webhook.URL != "" {
				chs = append(chs, channels.NewWebhookChannel(env.Notifier.Webhook.URL, env.Notifier.Webhook.Template))
			}
		}
	}
	if len(chs) == 0 {
		log.Warn("通知未启动")
		return nil
	}
	// 反馈签名密钥每次从 HotConfig 读，不在此钉死
	n := notifier.NewNotifier(db,
		notifier.NotifierOptions{MaxAttempts: cfg.Notifier.MaxAttempts, RetryBaseInterval: cfg.Notifier.RetryBaseInterval, HotConfig: rt},
		chs...)
	return pipeline.New(
		func(ctx context.Context, limit int) ([]models.RentPost, error) {
			requireAI := false
			cfg := rt.Get()
			env := rt.Secrets()
			if cfg.Filter.AIEnabled != nil && *cfg.Filter.AIEnabled && env.Filter.LLM.APIKey != "" {
				n, err := db.CountEnabledAIRules()
				if err != nil {
					return nil, err
				}
				requireAI = n > 0
			}
			return db.FetchNotifyBatch(channelNames(chs), limit, requireAI)
		},
		n.ProcessBatch,
		pipeline.Options{
			BatchSize: cfg.Notifier.BatchSize,
			Linger:    time.Duration(cfg.Notifier.RetryBaseInterval) * time.Second,
			Component: pkglog.Notifier,
			WaitFull:  true,
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

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
