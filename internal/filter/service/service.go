package service

import (
	"context"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/filter"
	"rent-scout/internal/filter/llm"
	"rent-scout/internal/models"
	"rent-scout/internal/pipeline"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

const (
	collectedCap = 64
	replayCap    = 1
	replayLimit  = 2000
)

// Options 筛选服务依赖
type Options struct {
	Config *config.HotConfig
	Store  *store.Store
}

// Service 硬筛、AI 筛和规则 replay
type Service struct {
	rt        *config.HotConfig
	db        *store.Store
	consumer  *filter.Consumer
	hard      *pipeline.Consumer[models.RentPost]
	ai        *pipeline.Consumer[models.RentPost]
	collected chan struct{}
	replay    chan struct{}
}

func New(opts Options) (*Service, error) {
	log := pkglog.Component(pkglog.Main)
	rt, db := opts.Config, opts.Store
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
		llmOpts := []llm.ClientOptions{{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: model}}
		for _, m := range env.Filter.LLM.FallbackModels {
			llmOpts = append(llmOpts, llm.ClientOptions{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: m})
		}
		pool := llm.NewPool(llmOpts, llm.PoolOptions{})
		ai = filter.NewAIBatchEvaluator(pool)
		log.Info("筛选 AI 已启用", "model", model)
	} else {
		log.Warn("筛选 AI 已关闭")
	}
	chain := filter.NewRuleChain(ai)
	fc := filter.NewConsumerWithOptions(chain, db, filter.ConsumerOptions{AIBatchSize: cfg.Filter.AIBatchSize})
	hardPipe := pipeline.New(
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
	return &Service{
		rt:        rt,
		db:        db,
		consumer:  fc,
		hard:      hardPipe,
		ai:        aiPipe,
		collected: make(chan struct{}, collectedCap),
		replay:    make(chan struct{}, replayCap),
	}, nil
}

// SignalCollected 采集入库后的非阻塞信号；满则丢
func (s *Service) SignalCollected() {
	if s == nil {
		return
	}
	select {
	case s.collected <- struct{}{}:
	default:
	}
}

// SignalRulesChanged 规则变更非阻塞信号；容量 1，满则丢
func (s *Service) SignalRulesChanged() {
	if s == nil {
		return
	}
	select {
	case s.replay <- struct{}{}:
	default:
	}
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil {
		<-ctx.Done()
		return nil
	}
	go s.bridgeCollected(ctx)
	if s.hard != nil {
		go s.hard.Run(ctx)
	}
	if s.ai != nil {
		go s.ai.Run(ctx)
	}
	s.runReplay(ctx)
	return nil
}

func (s *Service) bridgeCollected(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.collected:
			if s.hard != nil {
				s.hard.Signal()
			}
		}
	}
}

func (s *Service) runReplay(ctx context.Context) {
	log := pkglog.Component(pkglog.Filter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.replay:
			if s.consumer == nil {
				continue
			}
			cfg := s.rt.Get()
			now := time.Now()
			start, end, err := config.ResolveTimeRange(cfg.Collector.Douban.RangeFrom, "now", now)
			if err != nil {
				log.Warn("规则 replay 时间窗无效", "err", err)
				continue
			}
			posts, err := s.db.ListPublishedBetween(start, end, replayLimit)
			if err != nil {
				log.Error("规则 replay 拉帖失败", "err", err)
				continue
			}
			if err := s.consumer.ReplayHard(ctx, posts); err != nil {
				log.Error("规则 replay 失败", "err", err)
			}
		}
	}
}
