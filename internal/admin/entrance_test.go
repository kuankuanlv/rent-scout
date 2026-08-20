package admin

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestHTTPShutdownBudget(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := config.DefaultApp()
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	kv["setup.completed"] = "true"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	_ = rt.ReloadOnce()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	svc, err := New(Options{Config: rt, Store: s, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("HTTP 未起来: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Shutdown 超过 5s 预算")
	}
}
