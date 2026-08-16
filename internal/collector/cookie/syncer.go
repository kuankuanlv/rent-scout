package cookie

import (
	"context"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// DefaultSyncInterval CookieCloud 同步间隔；不进 admin 配置
const DefaultSyncInterval = 10 * time.Minute

// Syncer 独立协程：从 CookieCloud 拉 cookie 写入 kv 的 cookie_raw，采集侧只读
type Syncer struct {
	rt       *config.HotConfig
	db       *store.Store
	interval time.Duration
}

func NewSyncer(rt *config.HotConfig, db *store.Store, interval time.Duration) *Syncer {
	if interval <= 0 {
		interval = DefaultSyncInterval
	}
	return &Syncer{rt: rt, db: db, interval: interval}
}

// Run 先同步一次，再按间隔循环；ctx 取消退出
func (s *Syncer) Run(ctx context.Context) {
	log := pkglog.Component(pkglog.DoubanCookieCloud)
	if s.rt == nil || s.db == nil {
		log.Error("同步器未初始化")
		return
	}
	log.Info("CookieCloud 同步协程已启动", "interval", s.interval.String())
	s.syncOnce(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.syncOnce(ctx)
		}
	}
}

func (s *Syncer) syncOnce(ctx context.Context) {
	col := s.rt.Secrets().Collector
	s.syncSource(ctx, "douban", col.Douban)
	s.syncSource(ctx, "weibo", col.Weibo)
	s.syncSource(ctx, "weibo.cn", col.Weibo)
}

func (s *Syncer) syncSource(ctx context.Context, source string, dc config.DoubanCookieConfig) {
	log := pkglog.Component(pkglog.SourceCookieCloud(source))
	if config.ParseCookieMode(dc.CookieMode) != config.CookieModeCookieCloud {
		log.Info("当前配置 CookieCloud 未启用，无需执行")
		return
	}
	ins, err := InspectCookieCloudFor(ctx, dc, source)
	if err != nil {
		log.Error("同步失败", "err", err)
		return
	}
	rawKey := config.CookieRawKey(source)
	local := dc.CookieRaw
	if config.CookieSource(source) == "weibo.cn" {
		local = dc.CookieRawCN
	}
	if ins.Cookie == local {
		log.Info("cookie 无变化，不写库", "names", ins.Names)
		return
	}
	if err := store.SetConfigBatch(s.db, map[string]string{rawKey: ins.Cookie}); err != nil {
		log.Error("写库失败", "err", err)
		return
	}
	if err := s.rt.ReloadOnce(); err != nil {
		log.Error("热加载失败", "err", err)
		return
	}
	log.Info("已写入本地 cookie", "names", ins.Names, "count", len(ins.Names))
}
