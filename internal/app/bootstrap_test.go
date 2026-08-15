package app

import (
	"os"
	"path/filepath"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestBootstrapOK(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	res, cleanup, err := Bootstrap(Options{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if res.Store == nil || res.Config == nil {
		t.Fatal("resources 缺失")
	}
}

func TestBootstrapCreatesAdminToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	app := config.DefaultApp()
	app.Admin.AuthRequired = true
	app.Admin.Token = ""
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	s.Close()

	res, cleanup, err := Bootstrap(Options{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	tok := res.Config.Get().Admin.Token
	if tok == "" {
		t.Fatal("应自动生成 admin token")
	}
	m, _ := store.GetConfigMap(res.Store)
	if m["admin.token"] == "" {
		t.Fatal("token 应写库")
	}
}

func TestBootstrapOpenFail(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Bootstrap(Options{DBPath: filepath.Join(f, "db.sqlite")})
	if err == nil {
		t.Fatal("应失败")
	}
}
