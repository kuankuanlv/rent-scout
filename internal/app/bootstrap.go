package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Options bootstrap 入参
type Options struct {
	DBPath string
}

// Resources 顶层资源；只给 main 按字段拆给各模块，模块不要收整份
type Resources struct {
	Store  *store.Store
	Config *config.HotConfig
}

// Bootstrap 打开库、种子规则、首次加载配置、初始化日志、必要时补 admin token。不启动长期协程。
func Bootstrap(opts Options) (*Resources, func(), error) {
	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = os.Getenv("DB_PATH")
	}
	if dbPath == "" {
		dbPath = "db/rent-scout.db"
	}

	boot := pkglog.Component(pkglog.Main)

	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	cleanup := func() { _ = db.Close() }

	if err := db.EnsureDefaultRule(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("写入默认规则失败: %w", err)
	}

	cnt, err := store.ConfigCount(db)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("读取配置条数失败: %w", err)
	}
	if cnt == 0 {
		boot.Warn("配置为空，请完成引导", "hint", "visit /admin/setup")
	}

	rt := config.NewHotConfig(db)
	rt.SetAfterReload(func(app *config.AppConfig) {
		if app != nil {
			pkglog.SetHubCap(app.Log.MemoryLines)
		}
	})
	if err := rt.ReloadOnce(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("首次加载配置失败: %w", err)
	}
	cfg := rt.Get()

	_ = pkglog.New(pkglog.Options{
		Level:       cfg.Log.Level,
		Format:      cfg.Log.Format,
		Path:        cfg.Log.Path,
		MemoryLines: cfg.Log.MemoryLines,
	})
	boot = pkglog.Component(pkglog.Main)
	boot.Info("服务启动",
		"log_dir", pkglog.LogDir(),
		"addr", cfg.Server.Addr,
		"sources", cfg.Collector.Sources,
		"config_keys", cnt,
		"setup_done", store.IsSetupComplete(db),
	)

	adminToken := cfg.Admin.Token
	if cfg.Admin.AuthRequired && adminToken == "" {
		adminToken = randomHex(16)
		boot.Warn("已生成管理员 Token", "token_len", len(adminToken))
		_ = store.SetConfig(db, "admin.token", adminToken)
		_ = rt.ReloadOnce()
	}
	if !cfg.Admin.AuthRequired {
		boot.Warn("鉴权已关闭")
	}

	return &Resources{Store: db, Config: rt}, cleanup, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
