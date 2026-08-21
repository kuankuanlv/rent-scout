package core

import (
	"context"
	"errors"
	"net/http"
	"time"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Options 管理面 HTTP 服务依赖
type Options struct {
	Config         *config.HotConfig
	Store          *store.Store
	Sources        ports.SourceController
	OnRulesChanged func()
	NotifyManual   ports.NotifyManual
	Addr           string
}

// AdminService HTTP 监听与 5 秒优雅关闭
type AdminService struct {
	addr   string       // 监听地址（配置 server.addr，可被 Options.Addr 覆盖）
	server *Server      // 管理面路由与处理器
	http   *http.Server // 标准库 HTTP 服务器（ListenAndServe + Shutdown）
}

// --- 构造 ---

func New(opts Options) (*AdminService, error) {

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
	return &AdminService{
		addr:   addr,
		server: srv,
		http:   &http.Server{Addr: addr, Handler: srv.Handler()},
	}, nil
}

// --- 生命周期 ---

func (s *AdminService) Run(ctx context.Context) error {
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
