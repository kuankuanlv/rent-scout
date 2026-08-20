package filter

import (
	"context"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/config/window"
	"rent-scout/internal/filter/rule"
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
	rt            *config.HotConfig
	db            *store.Store
	consumer      *Consumer
	hard          *pipeline.Consumer[models.RentPost]
	ai            *pipeline.Consumer[models.RentPost]
	collected     chan struct{}
	replay        chan struct{}
	onNotifyReady func() // 筛选落库完成回调（通知消费器用来立即拉批）
}

func New(opts Options) (*Service, error) {
	rt, db := opts.Config, opts.Store
	chain := rule.NewRuleChain(nil)
	fc := NewConsumerWithOptions(chain, db, ConsumerOptions{HotConfig: rt})

	// late bind：pipe 构造在前，Service 赋值在后；筛选成功落库后才触发通知拉批信号
	var svc *Service
	notifyHard := func(ctx context.Context, batch []models.RentPost) error {
		if err := fc.ProcessHard(ctx, batch); err != nil {
			return err
		}
		if svc != nil && svc.onNotifyReady != nil {
			svc.onNotifyReady()
		}
		return nil
	}
	notifyAI := func(ctx context.Context, batch []models.RentPost) error {
		if err := fc.ProcessAI(ctx, batch); err != nil {
			return err
		}
		if svc != nil && svc.onNotifyReady != nil {
			svc.onNotifyReady()
		}
		return nil
	}

	hardPipe := pipeline.New(
		func(ctx context.Context, limit int) ([]models.RentPost, error) {
			if rt != nil {
				if n := rt.Get().Filter.BatchSize; n > 0 {
					limit = n
				}
			}
			return fc.FetchCollected(ctx, limit)
		},
		notifyHard,
		pipeline.Options{
			BatchSize: 20,
			Tick:      pipeline.DefaultTick,
			Component: pkglog.Filter,
			LiveBatchSize: func() int {
				if rt == nil {
					return 20
				}
				if n := rt.Get().Filter.BatchSize; n > 0 {
					return n
				}
				return 20
			},
		},
	)
	aiPipe := pipeline.New(
		fc.FetchAwaitingAI,
		notifyAI,
		pipeline.Options{
			BatchSize: 10,
			Tick:      pipeline.DefaultTick,
			Linger:    pipeline.DefaultLinger,
			Component: pkglog.AIReview,
			WaitFull:  true,
			LiveBatchSize: func() int {
				if rt == nil {
					return 10
				}
				if n := rt.Get().Filter.AIBatchSize; n > 0 {
					return n
				}
				return 10
			},
		},
	)
	svc = &Service{
		rt:        rt,
		db:        db,
		consumer:  fc,
		hard:      hardPipe,
		ai:        aiPipe,
		collected: make(chan struct{}, collectedCap),
		replay:    make(chan struct{}, replayCap),
	}
	return svc, nil
}

// SetOnNotifyReady 注册「筛选落库完成」回调（通知消费器用来立即拉批）
func (s *Service) SetOnNotifyReady(fn func()) {
	if s == nil {
		return
	}
	s.onNotifyReady = fn
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
			start, end, err := window.CollectorReplayWindow(cfg.Collector.Douban.RangeFrom, cfg.Collector.Weibo.RangeFrom, now)
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
			if s.ai != nil {
				s.ai.Signal()
			}
		}
	}
}
