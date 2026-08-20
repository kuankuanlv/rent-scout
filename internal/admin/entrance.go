package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Options 管理面 HTTP 服务依赖
type Options struct {
	Config         *config.HotConfig
	Store          *store.Store
	Sources        SourceController
	OnRulesChanged func()
	NotifyManual   NotifyManual
	Addr           string
}

// Service HTTP 监听与 5 秒关闭
type Service struct {
	addr   string
	server *Server
	http   *http.Server
}

func New(opts Options) (*Service, error) {
	addr := opts.Addr
	if addr == "" && opts.Config != nil {
		if app := opts.Config.Get(); app != nil {
			addr = app.Server.Addr
		}
	}
	srv := NewServer(opts.Store, opts.Config, opts.Sources)
	srv.SetOnRulesChanged(opts.OnRulesChanged)
	srv.SetCookieProbe(NewCookieProbe())
	srv.SetLLMProbe(NewLLMProbe())
	srv.SetNotifyProbe(NewNotifyProbe())
	srv.SetNotifyManual(opts.NotifyManual)
	return &Service{
		addr:   addr,
		server: srv,
		http:   &http.Server{Addr: addr, Handler: srv.Handler()},
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	log := pkglog.Component(pkglog.Main)
	go func() {
		log.Info("HTTP 开始监听", "addr", s.addr)
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务失败", "err", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		log.Warn("关闭超时", "err", err)
	}
	return nil
}
