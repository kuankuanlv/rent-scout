package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

func TestNewAlwaysHasRunner(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := config.DefaultApp()
	app.Collector.Sources = nil
	rt := config.NewHotConfigWithSnapshot(app, config.DefaultSecrets())
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Controller() == nil {
		t.Fatal("源全关也应有调度协程")
	}
	if svc.SourceEnabled(models.SourceDouban.String()) {
		t.Fatal("配置未勾选时不应视为启用")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未退出")
	}
}
