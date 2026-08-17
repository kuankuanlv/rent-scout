package service

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestServiceSetOnNotifyReady(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	rt := config.NewHotConfigWithSnapshot(config.DefaultApp(), config.DefaultSecrets())
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}

	var called int32
	svc.SetOnNotifyReady(func() {
		atomic.StoreInt32(&called, 1)
	})

	// 直接调用包装后的处理函数（模拟 pipeline 触发）
	// 注意：在实际代码中，我们需要在 New 里正确注入这些包装函数。
	// 这里我们先测试 SetOnNotifyReady 是否能安全设置。
	if svc.onNotifyReady == nil {
		t.Fatal("onNotifyReady should not be nil after SetOnNotifyReady")
	}

	svc.onNotifyReady()
	if atomic.LoadInt32(&called) != 1 {
		t.Error("callback was not called")
	}
}
