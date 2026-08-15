package cookie

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

func TestSyncerWritesCookieRaw(t *testing.T) {
	payload := `{"cookie_data":{"www.douban.com":[{"name":"bid","value":"synced","domain":".douban.com"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	app := config.DefaultApp()
	sec := config.DefaultSecrets()
	sec.Collector.Douban = config.DoubanCookieConfig{
		CookieMode:      config.CookieModeCookieCloud.String(),
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  "uuid",
		CookiecloudPass: "pass",
	}
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(sec))
	if err := store.SetConfigBatch(db, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(db)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}

	s := NewSyncer(rt, db, time.Hour)
	s.syncOnce(context.Background())

	got, err := store.GetConfig(db, config.KeyDoubanCookieRaw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "bid=synced" {
		t.Errorf("cookie_raw = %q, want bid=synced", got)
	}
	if rt.Secrets().Collector.Douban.CookieRaw != "bid=synced" {
		t.Errorf("热配置未刷新: %q", rt.Secrets().Collector.Douban.CookieRaw)
	}
}

func TestSyncerSkipsWhenNotCookieCloud(t *testing.T) {
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rt := config.NewHotConfigWithSnapshot(config.DefaultApp(), &config.Secrets{
		Collector: config.SecretsCollector{
			Douban: config.DoubanCookieConfig{
				CookieMode:     config.CookieModeRaw.String(),
				CookiecloudURL: srv.URL,
			},
		},
	})
	NewSyncer(rt, db, time.Hour).syncOnce(context.Background())
	if hit != 0 {
		t.Fatalf("raw 模式不应打 CookieCloud, hit=%d", hit)
	}
}

func TestSyncerSkipsWriteWhenUnchanged(t *testing.T) {
	payload := `{"cookie_data":{"www.douban.com":[{"name":"bid","value":"same","domain":".douban.com"}]}}`
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app := config.DefaultApp()
	sec := config.DefaultSecrets()
	sec.Collector.Douban = config.DoubanCookieConfig{
		CookieMode:      config.CookieModeCookieCloud.String(),
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  "uuid",
		CookiecloudPass: "pass",
	}
	if err := store.SetConfigBatch(db, config.MergeKV(config.AppToKV(app), config.SecretsToKV(sec))); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(db)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	s := NewSyncer(rt, db, time.Hour)
	s.syncOnce(context.Background())
	s.syncOnce(context.Background())
	if hits != 2 {
		t.Fatalf("应探测两次, hit=%d", hits)
	}
	hist, err := store.ListConfigHistory(db, 50)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range hist {
		if e.Key == config.KeyDoubanCookieRaw {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("无变化不应再写历史, cookie_raw 历史=%d", n)
	}
}
