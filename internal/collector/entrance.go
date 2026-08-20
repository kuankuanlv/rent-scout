package collector

import (
	"context"

	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

const postCreatedCap = 64

// Options 采集服务依赖
type Options struct {
	Config        *config.HotConfig
	Store         *store.Store
	Sources       []Source // 由 main 组装，避免本包 import 具体源造成循环依赖
	OnPostCreated func()
}

// CollectorService 采集调度 + Cookie 同步
type CollectorService struct {
	rt            *config.HotConfig // 热配置快照：源开关、间隔、时间窗等运行中读取
	db            *store.Store      // SQLite：采集进度、去重、帖子落库
	runner        *Runner           // 每源一条常驻循环的调度器
	syncer        *cookie.Syncer    // CookieCloud 定时同步登录态
	trigger       chan struct{}     // 新帖落库信号（容量 64，满则丢），bridge 转发给下游
	onPostCreated func()            // 新帖回调（通知消费器拉批）
}

// --- 构造 ---

func New(opts Options) (*CollectorService, error) {
	rt, db := opts.Config, opts.Store
	trigger := make(chan struct{}, postCreatedCap)
	runner := NewRunner(rt, db, opts.Sources, trigger)
	return &CollectorService{
		rt:            rt,
		db:            db,
		runner:        runner,
		syncer:        cookie.NewSyncer(rt, db, cookie.DefaultSyncInterval),
		trigger:       trigger,
		onPostCreated: opts.OnPostCreated,
	}, nil
}

// --- SourceController 契约（admin/ports.go 定义，管理台源控制）---

// Controller 管理台源控制；协程常驻，即使配置里源全关也返回自身
func (s *CollectorService) Controller() *CollectorService {
	if s == nil || s.runner == nil {
		return nil
	}
	return s
}

func (s *CollectorService) Sources() []string {
	if s == nil || s.runner == nil {
		return nil
	}
	return s.runner.Sources()
}

func (s *CollectorService) SetEnabled(name string, on bool) error {
	return s.runner.SetEnabled(name, on)
}

func (s *CollectorService) SourceEnabled(name string) bool {
	return s.runner.SourceEnabled(name)
}

// --- 生命周期 ---

func (s *CollectorService) Run(ctx context.Context) error {
	if s.runner != nil {
		go s.runner.Run(ctx)
	}
	go s.syncer.Run(ctx)
	go s.bridge(ctx)
	<-ctx.Done()
	return nil
}

// --- 内部协程 ---

func (s *CollectorService) bridge(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			if s.onPostCreated != nil {
				s.onPostCreated()
			}
		}
	}
}
