package config

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/store"
)

func TestPollTickAdvancesOnlyOnSuccess(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := DefaultApp()
	kv := MergeKV(AppToKV(app), SecretsToKV(DefaultSecrets()))
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := NewHotConfig(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	m, _ := store.GetConfigMap(s)
	h0 := hashKV(m)
	h1 := rt.pollTick(h0)
	if h1 != h0 {
		t.Fatalf("hash 未变应保持 lastHash: %d -> %d", h0, h1)
	}
	kv["server.addr"] = ":9999"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	h2 := rt.pollTick(h1)
	if h2 == h1 {
		t.Fatal("变更后应推进 hash")
	}
	if rt.Get().Server.Addr != ":9999" {
		t.Fatalf("addr=%q", rt.Get().Server.Addr)
	}
}
