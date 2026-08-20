package notifier

import (
	"context"
	"fmt"
	"rent-scout/internal/notifier/group"
	"strings"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier/channels"
	"rent-scout/internal/batch"
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
	rt   *config.HotConfig                  // 热配置：渠道开关、批大小、发送间隔
	db   *store.Store                       // SQLite：拉取待通知帖、写通知账本
	n    *Notifier                          // 通知核心：组批、渠道分发、账本
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
			BatchSize: config.DefaultNotifierBatch,
			Tick:      batch.DefaultTick,
			WaitFull:  true,
			Component: pkglog.Notifier,
			LiveBatchSize: func() int {
				if rt == nil {
					return config.DefaultNotifierBatch
				}
				if n := rt.Get().Notifier.BatchSize; n > 0 {
					return n
				}
				return config.DefaultNotifierBatch
			},
			LiveLinger: func() time.Duration {
				sec := config.DefaultNotifierInterval
				if rt != nil {
					if n := rt.Get().Notifier.Interval; n > 0 {
						sec = n
					}
				}
				return time.Duration(sec) * time.Second
			},
		},
	)
	return s, nil
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
	log := pkglog.Component(pkglog.Notifier)
	if s.rt == nil {
		log.Info("当前配置通知未启用，无需执行")
		return nil, nil
	}
	app := s.rt.Get()
	if app == nil {
		log.Info("当前配置通知未启用，无需执行")
		return nil, nil
	}
	// 每分钟探测日志：先打当前各渠道开关，再决定后续逻辑
	log.Info("当前通知配置：" + channelSwitchSummary(app, s.rt.Secrets()))
	if len(app.Notifier.Channels) == 0 {
		log.Info("当前配置通知未启用，无需执行")
		return nil, nil
	}
	chs := channels.Live(app, s.rt.Secrets())
	if len(chs) == 0 {
		log.Info("当前配置通知渠道密钥为空，无需执行")
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

// channelSwitchSummary 当前通知渠道开关摘要（每分钟探测日志用）：
// 渠道=勾选且密钥非空（与 channels.Live 同一判定），固定顺序输出
func channelSwitchSummary(app *config.AppConfig, env *config.Secrets) string {
	parts := make([]string, 0, len(channels.Names()))
	for _, name := range channels.Names() {
		parts = append(parts, fmt.Sprintf("%s=%t", name, channels.Enabled(app, env, name)))
	}
	return strings.Join(parts, "、")
}
