package service

import (
	"context"

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
	s := &Service{rt: rt, db: db}
	s.pipe = pipeline.New(
		s.fetch,
		n.ProcessBatch,
		pipeline.Options{
			BatchSize: 20,
			Linger:    pipeline.DefaultLinger,
			Component: pkglog.Notifier,
			WaitFull:  true,
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
	if app == nil || len(app.Notifier.Channels) == 0 {
		log.Info("当前配置通知未启用，无需执行")
		return nil, nil
	}
	chs := liveChannels(app, s.rt.Secrets())
	if len(chs) == 0 {
		log.Info("当前配置通知渠道密钥为空，无需执行")
		return nil, nil
	}
	if n := app.Notifier.BatchSize; n > 0 {
		limit = n
	}
	names := make([]string, len(chs))
	for i, c := range chs {
		names[i] = c.Name()
	}
	// AI 开启时只通知已 AI 审核过的帖（通过/未通过都发，等 ai_result 落库）；未开启直接通知 passed
	requireAI := app.Filter.AIEnabled != nil && *app.Filter.AIEnabled
	return s.db.FetchNotifyBatch(names, limit, requireAI)
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

func (s *Service) Enabled() bool {
	return s != nil && s.pipe != nil
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.pipe == nil {
		<-ctx.Done()
		return nil
	}
	s.pipe.Run(ctx)
	return nil
}
