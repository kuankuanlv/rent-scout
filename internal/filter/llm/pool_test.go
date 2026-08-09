package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 主模型 429 → 自动 fallback 到备用模型
func TestPoolFallback(t *testing.T) {
	var mainCalls, backupCalls atomic.Int32
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mainCalls.Add(1)
		w.WriteHeader(429)
	}))
	defer main.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer backup.Close()

	p := NewPool([]ClientOptions{
		{BaseURL: main.URL, APIKey: "k", Model: "m1"},
		{BaseURL: backup.URL, APIKey: "k", Model: "m2"},
	}, PoolOptions{MaxFailures: 3, CircuitDuration: time.Hour})
	out, err := p.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Errorf("out = %q", out)
	}
	if mainCalls.Load() != 1 || backupCalls.Load() != 1 {
		t.Errorf("fallback 调用数: main=%d backup=%d", mainCalls.Load(), backupCalls.Load())
	}
}

// 连续失败熔断：达到阈值后直接失败（不再请求），熔断期过后恢复
func TestPoolCircuitBreaker(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	p := NewPool([]ClientOptions{{BaseURL: srv.URL, APIKey: "k", Model: "m"}}, PoolOptions{MaxFailures: 2, CircuitDuration: time.Hour})
	// 两次失败 → 熔断
	for i := 0; i < 2; i++ {
		if _, err := p.Chat(context.Background(), "s", "u"); err == nil {
			t.Fatal("应失败")
		}
	}
	// 熔断中：不再请求
	callsBefore := calls.Load()
	if _, err := p.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("熔断中应直接失败")
	}
	if calls.Load() != callsBefore {
		t.Errorf("熔断中不应发请求: %d → %d", callsBefore, calls.Load())
	}
	// 熔断恢复：手动拨快时钟不可行——验证短熔断恢复
	p2 := NewPool([]ClientOptions{{BaseURL: srv.URL, APIKey: "k", Model: "m"}}, PoolOptions{MaxFailures: 2, CircuitDuration: 50 * time.Millisecond})
	_, _ = p2.Chat(context.Background(), "s", "u")
	_, _ = p2.Chat(context.Background(), "s", "u")
	time.Sleep(80 * time.Millisecond) // 熔断期过
	if _, err := p2.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("恢复后仍失败（500 服务器）——应报错但已发请求")
	}
}
