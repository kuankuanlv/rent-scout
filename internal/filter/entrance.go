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

// FilterService 硬筛、AI 筛和规则 replay
type FilterService struct {
	rt            *config.HotConfig                  // 热配置：批大小、AI 开关等运行中读取
	db            *store.Store                       // SQLite：拉帖、回写 AI 结果
	consumer      *Consumer                          // 硬筛/AI 筛执行器
	hard          *pipeline.Consumer[models.RentPost] // 硬筛管道（新帖 → 硬规则）
	ai            *pipeline.Consumer[models.RentPost] // AI 筛管道（pending → LLM 审核）
	collected     chan struct{}                      // 采集入库信号（容量 collectedCap，满则丢）
	replay        chan struct{}                      // 规则变更 replay 信号（容量 1，满则丢）
	onNotifyReady func()                             // 筛选落库完成回调（通知消费器立即拉批）
}

// --- 构造 ---

func New(opts Options) (*FilterService, error) {
	rt, db := opts.Config, opts.Store
	chain := rule.NewRuleChain(nil)
	fc := NewConsumerWithOptions(chain, db, ConsumerOptions{HotConfig: rt})

	// late bind：pipe 构造在前，FilterService 赋值在后；筛选成功落库后才触发通知拉批信号
	var svc *FilterService
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
	svc = &FilterService{
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

// --- 回调与信号接口 ---

// SetOnNotifyReady 注册「筛选落库完成」回调（通知消费器用来立即拉批）
func (s *FilterService) SetOnNotifyReady(fn func()) {
	if s == nil {
		return
	}
	s.onNotifyReady = fn
}

// SignalCollected 采集入库后的非阻塞信号；满则丢
func (s *FilterService) SignalCollected() {
	if s == nil {
		return
	}
	select {
	case s.collected <- struct{}{}:
	default:
	}
}

// SignalRulesChanged 规则变更非阻塞信号；容量 1，满则丢
func (s *FilterService) SignalRulesChanged() {
	if s == nil {
		return
	}
	select {
	case s.replay <- struct{}{}:
	default:
	}
}

// --- 生命周期 ---

func (s *FilterService) Run(ctx context.Context) error {
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

// --- 内部协程 ---

func (s *FilterService) bridgeCollected(ctx context.Context) {
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

func (s *FilterService) runReplay(ctx context.Context) {
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
