package config

import (
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"rent-scout/internal/store"
)

// HotConfig 并发安全的热配置容器：独立 goroutine 轮询 DB，COW 替换快照；业务只读 Get/Secrets
type HotConfig struct {
	appPtr      atomic.Pointer[AppConfig]
	secretsPtr  atomic.Pointer[Secrets]
	db          *store.Store
	mu          sync.Mutex
	afterReload func(*AppConfig)
}

// SetAfterReload 重载成功后回调（如同步日志 ring 容量）；须在 WatchDB 前登记
func (h *HotConfig) SetAfterReload(fn func(*AppConfig)) {
	h.mu.Lock()
	h.afterReload = fn
	h.mu.Unlock()
}

func (h *HotConfig) fireAfterReload(app *AppConfig) {
	h.mu.Lock()
	fn := h.afterReload
	h.mu.Unlock()
	if fn != nil && app != nil {
		fn(app)
	}
}

// NewHotConfig 创建热配置容器
func NewHotConfig(db *store.Store) *HotConfig {
	h := &HotConfig{db: db}
	h.appPtr.Store(DefaultApp())
	h.secretsPtr.Store(DefaultSecrets())
	return h
}

// NewHotConfigWithSnapshot 测试用：直接注入快照
func NewHotConfigWithSnapshot(app *AppConfig, secrets *Secrets) *HotConfig {
	h := &HotConfig{}
	if app == nil {
		app = DefaultApp()
	}
	if secrets == nil {
		secrets = DefaultSecrets()
	}
	h.appPtr.Store(app)
	h.secretsPtr.Store(secrets)
	return h
}

// Get 返回当前公开配置快照（只读，无锁）
func (h *HotConfig) Get() *AppConfig {
	return h.appPtr.Load()
}

// Secrets 返回当前敏感配置快照（只读，无锁）
func (h *HotConfig) Secrets() *Secrets {
	return h.secretsPtr.Load()
}

// FeedbackSecret 当前反馈签名密钥：鉴权开则用 admin.token，否则空
func (h *HotConfig) FeedbackSecret() string {
	app := h.Get()
	if app == nil || !app.Admin.AuthRequired {
		return ""
	}
	return app.Admin.Token
}

// ReloadOnce 从 SQLite 重载；COW 替换指针，读者无锁
func (h *HotConfig) ReloadOnce() error {
	// 不能 import pkglog（循环依赖）；duty 与 pkglog.HotConfig 同为 hot_config_reload
	log := slog.With("duty", "hot_config_reload")
	kv, err := store.GetConfigMap(h.db)
	if err != nil {
		log.Warn("配置重载失败", "err", err)
		return err
	}
	if len(kv) == 0 {
		app := DefaultApp()
		h.appPtr.Store(app)
		h.secretsPtr.Store(DefaultSecrets())
		h.fireAfterReload(app)
		log.Info("配置变更，开始 COW 更换快照", "source", "defaults", "keys", 0)
		return nil
	}
	app := KVToApp(kv)
	sec := KVToSecrets(kv)
	h.appPtr.Store(app)
	h.secretsPtr.Store(sec)
	h.fireAfterReload(app)
	log.Info("配置变更，开始 COW 更换快照", "source", "sqlite", "keys", len(kv), "addr", app.Server.Addr)
	return nil
}

// WatchDB 专用协程定时轮询 SQLite；变更时 COW 替换快照
func (h *HotConfig) WatchDB(interval time.Duration) func() {
	stop := make(chan struct{})
	var once sync.Once
	log := slog.With("duty", "hot_config_reload")
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var lastHash uint64
		// 启动先拉一次，避免 lastHash=0 重复打日志
		if kv, err := store.GetConfigMap(h.db); err == nil {
			lastHash = hashKV(kv)
		}
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				kv, err := store.GetConfigMap(h.db)
				if err != nil {
					log.Warn("配置重载失败", "err", err)
					continue
				}
				hash := hashKV(kv)
				if hash != lastHash {
					// 失败不推进 lastHash，下次还能重试
					if err := h.ReloadOnce(); err == nil {
						lastHash = hash
					}
				} else {
					// hash 未变跳过；Debug 避免 10s 刷屏
					log.Debug("配置 hash 未变，跳过 COW")
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

func hashKV(kv map[string]string) uint64 {
	// 先排序 key，避免 map 遍历无序导致同内容不同 hash
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var h uint64
	for _, k := range keys {
		for _, c := range k + kv[k] {
			h = h*31 + uint64(c)
		}
	}
	return h
}
