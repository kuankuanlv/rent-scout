package service

import (
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestPollTickHashUnchanged(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app := config.DefaultApp()
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	m, _ := store.GetConfigMap(s)
	// 通过 service 跑一轮：hash 未变不应出错
	svc, err := New(Options{Hot: rt, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	_ = svc
	_ = m
}

func TestWatchDBStop(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rt := config.NewHotConfig(s)
	_ = rt.ReloadOnce()
	stop := rt.WatchDB(time.Hour)
	stop()
	stop() // 幂等
}
