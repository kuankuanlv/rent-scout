package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier"
	"rent-scout/internal/notifier/channels"
	"rent-scout/internal/pipeline"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Options 通知服务依赖
type Options struct {
	Config *config.HotConfig
	Store  *store.Store
}

// Service 通知 pipeline；协程常驻，每轮自己读热配置
type Service struct {
	rt   *config.HotConfig
	db   *store.Store
	n    *notifier.Notifier
	pipe *pipeline.Consumer[models.RentPost]
}

func New(opts Options) (*Service, error) {
	rt, db := opts.Config, opts.Store
	live := func() []notifier.Channel {
		if rt == nil {
			return nil
		}
		return liveChannels(rt.Get(), rt.Secrets())
	}
	n := notifier.NewNotifier(db, notifier.NotifierOptions{HotConfig: rt, LiveChannels: live})
	s := &Service{rt: rt, db: db, n: n}

	s.pipe = pipeline.New(
		s.fetch,
		n.ProcessBatch,
		pipeline.Options{
			BatchSize: config.DefaultNotifierBatch,
			Tick:      pipeline.DefaultTick,
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

func (s *Service) fetch(ctx context.Context, limit int) ([]models.RentPost, error) {
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
	chs := liveChannels(app, s.rt.Secrets())
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
// 渠道=勾选且密钥非空（与 liveChannels 同一判定），固定顺序输出
func channelSwitchSummary(app *config.AppConfig, env *config.Secrets) string {
	on := map[string]bool{}
	for _, c := range liveChannels(app, env) {
		on[c.Name()] = true
	}
	order := []string{
		notifier.ChannelFeishu, notifier.ChannelDingtalk, notifier.ChannelWecom,
		notifier.ChannelPushplus, notifier.ChannelServerchan, notifier.ChannelWebhook,
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s=%t", name, on[name]))
	}
	return strings.Join(parts, "、")
}

func liveChannels(app *config.AppConfig, env *config.Secrets) []notifier.Channel {
	if app == nil || env == nil {
		return nil
	}
	n := env.Notifier
	var chs []notifier.Channel
	for _, name := range app.Notifier.Channels {
		switch name {
		case notifier.ChannelFeishu:
			if n.Feishu.Webhook != "" {
				chs = append(chs, channels.NewFeishuChannel(n.Feishu.Webhook))
			}
		case notifier.ChannelDingtalk:
			if n.Dingtalk.Webhook != "" {
				chs = append(chs, channels.NewDingtalkChannel(n.Dingtalk.Webhook, n.Dingtalk.Secret))
			}
		case notifier.ChannelWecom:
			if n.Wecom.Webhook != "" {
				chs = append(chs, channels.NewWecomChannel(n.Wecom.Webhook))
			}
		case notifier.ChannelPushplus:
			if n.Pushplus.Token != "" {
				chs = append(chs, channels.NewPushplusChannel("", n.Pushplus.Token, n.Pushplus.Topic))
			}
		case notifier.ChannelServerchan:
			if n.Serverchan.Sendkey != "" {
				chs = append(chs, channels.NewServerchanChannel(n.Serverchan.Sendkey))
			}
		case notifier.ChannelWebhook:
			if n.Webhook.URL != "" {
				chs = append(chs, channels.NewWebhookChannel(n.Webhook.URL, n.Webhook.Template))
			}
		}
	}
	return chs
}

// Signal 上游落库后的非阻塞信号（满批立刻发；不足批继续等 interval）
func (s *Service) Signal() {
	if s == nil || s.pipe == nil {
		return
	}
	s.pipe.Signal()
}

func (s *Service) Enabled() bool {
	return s != nil && s.pipe != nil
}

const manualNotifyMax = 50

// SendSelected 控制台勾选直发；group 空则用「手动触发-MMddHH:mm:ss」
func (s *Service) SendSelected(ctx context.Context, ids []int64, group string) error {
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
	if strings.TrimSpace(group) == "" {
		group = notifier.ManualGroupName(time.Now())
	}
	return s.n.ProcessManual(ctx, posts, group)
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.pipe == nil {
		<-ctx.Done()
		return nil
	}
	s.pipe.Run(ctx)
	return nil
}
