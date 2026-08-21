package notifier

import (
	"context"
	"fmt"
	"rent-scout/internal/notifier/group"
	"strings"
	"time"

	"rent-scout/internal/batch"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier/channels"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Options 通知服务依赖
type Options struct {
	Config *config.HotConfig
	Store  *store.Store
}

// NotifierService 通知 batch；协程常驻，每轮自己读热配置
type NotifierService struct {
	rt   *config.HotConfig                // 热配置：渠道开关、批大小、发送间隔
	db   *store.Store                     // SQLite：拉取待通知帖、写通知账本
	n    *Notifier                        // 通知核心：组批、渠道分发、账本
	pipe *batch.Consumer[models.RentPost] // 拉批管道（fetch → ProcessBatch）
}

// --- 构造 ---

func New(opts Options) (*NotifierService, error) {
	rt, db := opts.Config, opts.Store
	live := func() []Channel {
		if rt == nil {
			return nil
		}
		return channels.Live(rt.Get(), rt.Secrets())
	}
	n := NewNotifier(db, NotifierOptions{HotConfig: rt, LiveChannels: live})
	s := &NotifierService{rt: rt, db: db, n: n}

	s.pipe = batch.New(
		s.fetch,
		n.ProcessBatch,
		batch.Options{
			BatchSize:     config.DefaultNotifierBatch,
			Tick:          batch.DefaultTick,
			WaitFull:      true,
			Component:     pkglog.Notifier,
			LiveBatchSize: s.liveBatchSize,
			LiveLinger:    s.liveLinger,
			// 每轮探测只打这一条：体现当前开启了什么渠道、凑批节奏（满批立发，不足批最多等 linger）
			TickLog: func() {
				lg := pkglog.Component(pkglog.Notifier)
				var app *config.AppConfig
				var env *config.Secrets
				if rt != nil {
					app = rt.Get()
					env = rt.Secrets()
				}
				lg.Info("通知探测：满批立发，不足批最多等 linger",
					"channels", enabledChannelsSummary(app, env),
					"batch", s.liveBatchSize(),
					"linger", s.liveLinger().String(),
				)
			},
		},
	)
	return s, nil
}

// liveBatchSize 当前凑批大小（热读；与 batch.Options.LiveBatchSize 同源）
func (s *NotifierService) liveBatchSize() int {
	if s.rt == nil {
		return config.DefaultNotifierBatch
	}
	if n := s.rt.Get().Notifier.BatchSize; n > 0 {
		return n
	}
	return config.DefaultNotifierBatch
}

// liveLinger 当前不足批最长等待（notifier.interval，秒；热读）
func (s *NotifierService) liveLinger() time.Duration {
	sec := config.DefaultNotifierInterval
	if s.rt != nil {
		if n := s.rt.Get().Notifier.Interval; n > 0 {
			sec = n
		}
	}
	return time.Duration(sec) * time.Second
}

// --- batch 信号接口 ---

// Signal 上游落库后的非阻塞信号（满批立刻发；不足批继续等 interval）
func (s *NotifierService) Signal() {
	if s == nil || s.pipe == nil {
		return
	}
	s.pipe.Signal()
}

func (s *NotifierService) Enabled() bool {
	return s != nil && s.pipe != nil
}

// --- 控制台手动发送 ---

const manualNotifyMax = 50

// SendSelected 控制台勾选直发；group 空则用「手动触发-MMddHH:mm:ss」
func (s *NotifierService) SendSelected(ctx context.Context, ids []int64, groupName string) error {
	if s == nil || s.n == nil {
		return fmt.Errorf("通知未配置")
	}
	if len(ids) > manualNotifyMax {
		ids = ids[:manualNotifyMax]
	}
	posts, err := s.db.ListPostsByIDs(ids)
	if err != nil {
		return err
	}
	if len(posts) == 0 {
		return fmt.Errorf("没有可发送的帖子")
	}
	if strings.TrimSpace(groupName) == "" {
		groupName = group.ManualGroupName(time.Now())
	}
	return s.n.ProcessManual(ctx, posts, groupName)
}

// --- 生命周期 ---

func (s *NotifierService) Run(ctx context.Context) error {
	if s == nil || s.pipe == nil {
		<-ctx.Done()
		return nil
	}
	s.pipe.Run(ctx)
	return nil
}

// --- 内部：batch 拉批回调 ---

func (s *NotifierService) fetch(ctx context.Context, limit int) ([]models.RentPost, error) {
	if s.rt == nil {
		return nil, nil
	}
	app := s.rt.Get()
	if app == nil {
		return nil, nil
	}
	// 当前状态（渠道开关/凑批节奏）由 TickLog 的单条「通知探测」日志体现，这里不再重复打
	if len(app.Notifier.Channels) == 0 {
		return nil, nil
	}
	chs := channels.Live(app, s.rt.Secrets())
	if len(chs) == 0 {
		return nil, nil
	}

	names := make([]string, len(chs))
	for i, c := range chs {
		names[i] = c.Name()
	}
	// AI 开启时只通知已 AI 审核过的帖（通过/未通过都发，等 ai_result 落库）；未开启直接通知 passed
	requireAI := app.Filter.AIEnabled != nil && *app.Filter.AIEnabled
	return s.db.FetchNotifyBatch(names, limit, requireAI)
}

// enabledChannelsSummary 当前已开启的通知渠道（勾选且密钥非空，与 channels.Live 同一判定）；
// 全关或配置不可用时返回「无」。探测单行日志用。
func enabledChannelsSummary(app *config.AppConfig, env *config.Secrets) string {
	if app == nil || env == nil {
		return "无"
	}
	names := make([]string, 0, len(channels.Names()))
	for _, name := range channels.Names() {
		if channels.Enabled(app, env, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, ",")
}
