package service

import (
	"context"
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

// Service 通知 pipeline
type Service struct {
	pipe     *pipeline.Consumer[models.RentPost]
	channels []string
}

func New(opts Options) (*Service, error) {
	log := pkglog.Component(pkglog.Main)
	rt, db := opts.Config, opts.Store
	cfg := rt.Get()
	env := rt.Secrets()
	chs := buildChannels(cfg.Notifier.Channels, env.Notifier)
	if len(chs) == 0 {
		log.Warn("通知未启动")
		return &Service{}, nil
	}
	n := notifier.NewNotifier(db,
		notifier.NotifierOptions{MaxAttempts: cfg.Notifier.MaxAttempts, RetryBaseInterval: cfg.Notifier.RetryBaseInterval, HotConfig: rt},
		chs...)
	names := channelNames(chs)
	pipe := pipeline.New(
		func(ctx context.Context, limit int) ([]models.RentPost, error) {
			return db.FetchNotifyBatch(names, limit)
		},
		n.ProcessBatch,
		pipeline.Options{
			BatchSize: cfg.Notifier.BatchSize,
			Linger:    time.Duration(cfg.Notifier.RetryBaseInterval) * time.Second,
			Component: pkglog.Notifier,
			WaitFull:  true,
		},
	)
	return &Service{pipe: pipe, channels: names}, nil
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

func buildChannels(enabled []string, env config.SecretsNotifier) []notifier.Channel {
	var chs []notifier.Channel
	for _, name := range enabled {
		switch name {
		case notifier.ChannelFeishu:
			if env.Feishu.Webhook != "" {
				chs = append(chs, channels.NewFeishuChannel(env.Feishu.Webhook))
			}
		case notifier.ChannelDingtalk:
			if env.Dingtalk.Webhook != "" {
				chs = append(chs, channels.NewDingtalkChannel(env.Dingtalk.Webhook, env.Dingtalk.Secret))
			}
		case notifier.ChannelWecom:
			if env.Wecom.Webhook != "" {
				chs = append(chs, channels.NewWecomChannel(env.Wecom.Webhook))
			}
		case notifier.ChannelPushplus:
			if env.Pushplus.Token != "" {
				chs = append(chs, channels.NewPushplusChannel("", env.Pushplus.Token, env.Pushplus.Topic))
			}
		case notifier.ChannelServerchan:
			if env.Serverchan.Sendkey != "" {
				chs = append(chs, channels.NewServerchanChannel(env.Serverchan.Sendkey))
			}
		case notifier.ChannelWebhook:
			if env.Webhook.URL != "" {
				chs = append(chs, channels.NewWebhookChannel(env.Webhook.URL, env.Webhook.Template))
			}
		}
	}
	return chs
}

func channelNames(chs []notifier.Channel) []string {
	names := make([]string, len(chs))
	for i, c := range chs {
		names[i] = c.Name()
	}
	return names
}

// configuredChannelNames 已配密钥的渠道名，顺序与常量清单一致（原 EnabledChannels）
func configuredChannelNames(env config.SecretsNotifier) []string {
	var names []string
	if env.Feishu.Webhook != "" {
		names = append(names, notifier.ChannelFeishu)
	}
	if env.Dingtalk.Webhook != "" {
		names = append(names, notifier.ChannelDingtalk)
	}
	if env.Wecom.Webhook != "" {
		names = append(names, notifier.ChannelWecom)
	}
	if env.Pushplus.Token != "" {
		names = append(names, notifier.ChannelPushplus)
	}
	if env.Serverchan.Sendkey != "" {
		names = append(names, notifier.ChannelServerchan)
	}
	if env.Webhook.URL != "" {
		names = append(names, notifier.ChannelWebhook)
	}
	return names
}
