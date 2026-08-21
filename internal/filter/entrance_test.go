package filter

import (
	"path/filepath"
	"testing"

	"rent-scout/internal/batch"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestSignalDropWhenFull(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rt := config.NewHotConfigWithSnapshot(config.DefaultApp(), config.DefaultSecrets())
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < collectedCap+20; i++ {
		svc.SignalCollected()
	}
	if len(svc.collected) != collectedCap {
		t.Fatalf("collected len=%d want %d", len(svc.collected), collectedCap)
	}
	for i := 0; i < 10; i++ {
		svc.SignalRulesChanged()
	}
	if len(svc.replay) != replayCap {
		t.Fatalf("replay len=%d want %d", len(svc.replay), replayCap)
	}
}

func TestPipelineWaitFullFlags(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := config.DefaultApp()
	rt := config.NewHotConfigWithSnapshot(app, config.DefaultSecrets())
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	_ = batch.DefaultLinger
	if svc.hard == nil || svc.ai == nil {
		t.Fatal("batch 应构造")
	}
}
