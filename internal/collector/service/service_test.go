package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestNewWithoutSources(t *testing.T) {
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
	if svc.Controller() != nil {
		t.Fatal("无源时 Controller 应为 nil")
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
