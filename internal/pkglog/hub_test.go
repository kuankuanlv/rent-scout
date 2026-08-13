package pkglog

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

func TestHubRecentAndSubscribe(t *testing.T) {
	ResetHubForTest()
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&dutyHandler{inner: inner, json: false}))

	ch, cancel := SubscribeLogs()
	defer cancel()

	Component(Admin).Info("hello hub", "k", "v")

	recent := RecentLogs(10)
	if len(recent) != 1 {
		t.Fatalf("recent len=%d", len(recent))
	}
	if recent[0].Duty != Admin || recent[0].Message != "hello hub" {
		t.Fatalf("recent=%+v", recent[0])
	}
	if recent[0].Attrs != "k=v" {
		t.Fatalf("attrs=%q", recent[0].Attrs)
	}

	select {
	case line := <-ch:
		if line.Seq != recent[0].Seq || line.Message != "hello hub" {
			t.Fatalf("sub=%+v", line)
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe 超时")
	}
}

func TestSetHubCapShrinks(t *testing.T) {
	ResetHubForTest()
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&dutyHandler{inner: inner, json: false}))
	for i := 0; i < 5; i++ {
		Component(Admin).Info("n", "i", i)
	}
	SetHubCap(100) // 下限 100，5 条都还在
	if HubCap() != 100 {
		t.Fatalf("cap=%d", HubCap())
	}
	if n := len(RecentLogs(0)); n != 5 {
		t.Fatalf("len=%d", n)
	}
}

func TestHubRingDropsOldest(t *testing.T) {
	old := defaultHub
	defaultHub = newHub(3)
	t.Cleanup(func() { defaultHub = old })

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&dutyHandler{inner: inner, json: false}))
	for i := 0; i < 5; i++ {
		Component(Admin).Info("n", "i", i)
	}
	got := RecentLogs(10)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Attrs != "i=2" || got[2].Attrs != "i=4" {
		t.Fatalf("got=%+v", got)
	}
}
