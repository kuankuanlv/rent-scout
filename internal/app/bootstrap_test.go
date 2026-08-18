package app

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBootstrapAuthRequiredNoTokenFails(t *testing.T) {
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
	if err == nil {
		if cleanup != nil {
			defer cleanup()
		}
		t.Fatalf("应失败：鉴权开启但 token 为空时不允许启动")
	}
	if res != nil {
		t.Fatalf("resources 应为 nil，got=%v", res)
	}
}

func TestBootstrapLogsAdminToken(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "logs")
	t.Setenv("LOG_DIR", logDir)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	const wantTok = "my-startup-token-abc"
	app := config.DefaultApp()
	app.Admin.AuthRequired = true
	app.Admin.Token = wantTok
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
	if res.Config.Get().Admin.Token != wantTok {
		t.Fatalf("token = %q, want %q", res.Config.Get().Admin.Token, wantTok)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var logBody strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "rent-scout-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(logDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		logBody.Write(b)
	}
	text := logBody.String()
	if !strings.Contains(text, wantTok) {
		t.Errorf("启动日志应打印 token，got:\n%s", text)
	}
	if !strings.Contains(text, "管理台访问令牌") {
		t.Errorf("启动日志应含管理台访问令牌，got:\n%s", text)
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
