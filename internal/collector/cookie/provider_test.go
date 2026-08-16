package cookie

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

// file 模式已移除：应报错
func TestFileCookieProviderRejected(t *testing.T) {
	if _, err := New("file", config.DoubanCookieConfig{CookieFile: "/tmp/x"}); err == nil {
		t.Fatal("file 模式应报错")
	}
}

// none 模式：匿名（返回空串）
func TestNoneCookieProvider(t *testing.T) {
	p, err := New("none", config.DoubanCookieConfig{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "douban")
	if err != nil || got != "" {
		t.Errorf("none 模式应返回空: %q %v", got, err)
	}
}

// raw 模式：返回配置原文
func TestRawCookieProvider(t *testing.T) {
	p, err := New("raw", config.DoubanCookieConfig{CookieRaw: " dbcl2=abc; bid=xyz "})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "douban")
	if err != nil {
		t.Fatal(err)
	}
	if got != "dbcl2=abc; bid=xyz" {
		t.Errorf("Get = %q", got)
	}
}

// HotConfig provider：每次 Get 读最新 Secrets
func TestHotConfigCookieProviderFollowsSecrets(t *testing.T) {
	env := &config.Secrets{
		Collector: config.SecretsCollector{
			Douban: config.DoubanCookieConfig{CookieMode: "raw", CookieRaw: "a=1"},
		},
	}
	rt := config.NewHotConfigWithSnapshot(nil, env)
	p := NewHotConfigProvider(rt)
	got, err := p.Get(context.Background(), "douban")
	if err != nil || got != "a=1" {
		t.Fatalf("首次 Get = %q %v", got, err)
	}
	env.Collector.Douban.CookieRaw = "b=2"
	got, err = p.Get(context.Background(), "douban")
	if err != nil || got != "b=2" {
		t.Fatalf("改 raw 后 Get = %q %v", got, err)
	}
	env.Collector.Douban.CookieMode = "none"
	got, err = p.Get(context.Background(), "douban")
	if err != nil || got != "" {
		t.Fatalf("改 none 后 Get = %q %v", got, err)
	}
}

func TestHotConfigCookieProviderPerSource(t *testing.T) {
	env := &config.Secrets{
		Collector: config.SecretsCollector{
			Douban: config.DoubanCookieConfig{CookieMode: "raw", CookieRaw: "db=1"},
			Weibo:  config.DoubanCookieConfig{CookieMode: "raw", CookieRaw: "wb=1"},
		},
	}
	p := NewHotConfigProvider(config.NewHotConfigWithSnapshot(nil, env))
	got, err := p.Get(context.Background(), "weibo")
	if err != nil || got != "wb=1" {
		t.Fatalf("weibo Get = %q %v", got, err)
	}
	got, err = p.Get(context.Background(), "douban")
	if err != nil || got != "db=1" {
		t.Fatalf("douban Get = %q %v", got, err)
	}
}

func TestHotConfigWeiboCNCookie(t *testing.T) {
	env := &config.Secrets{
		Collector: config.SecretsCollector{
			Weibo: config.DoubanCookieConfig{CookieMode: "raw", CookieRaw: "wb=pc", CookieRawCN: "wb=mobi"},
		},
	}
	p := NewHotConfigProvider(config.NewHotConfigWithSnapshot(nil, env))
	got, err := p.Get(context.Background(), "weibo")
	if err != nil || got != "wb=pc" {
		t.Fatalf("weibo = %q %v", got, err)
	}
	got, err = p.Get(context.Background(), "weibo.cn")
	if err != nil || got != "wb=mobi" {
		t.Fatalf("weibo.cn = %q %v", got, err)
	}
}

// 未知模式：报错（配置错误应显式暴露）
func TestUnknownCookieMode(t *testing.T) {
	if _, err := New("weird", config.DoubanCookieConfig{}); err == nil {
		t.Fatal("未知模式应报错")
	}
}

func TestHotConfigCookieCloudReadsLocalOnly(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	env := &config.Secrets{
		Collector: config.SecretsCollector{
			Douban: config.DoubanCookieConfig{
				CookieMode:      config.CookieModeCookieCloud.String(),
				CookieRaw:       "dbcl2=local-only",
				CookiecloudURL:  srv.URL,
				CookiecloudKey:  "uuid",
				CookiecloudPass: "pass",
			},
		},
	}
	p := NewHotConfigProvider(config.NewHotConfigWithSnapshot(nil, env))
	got, err := p.Get(context.Background(), "douban")
	if err != nil {
		t.Fatal(err)
	}
	if got != "dbcl2=local-only" {
		t.Errorf("应读本地 cookie, got %q", got)
	}
	if hit {
		t.Fatal("采集 Get 禁止请求 CookieCloud")
	}
}

func TestHotConfigCookieCloudEmptyIsError(t *testing.T) {
	env := &config.Secrets{
		Collector: config.SecretsCollector{
			Douban: config.DoubanCookieConfig{CookieMode: config.CookieModeCookieCloud.String()},
		},
	}
	p := NewHotConfigProvider(config.NewHotConfigWithSnapshot(nil, env))
	_, err := p.Get(context.Background(), "douban")
	if err != ErrCookieMissing {
		t.Fatalf("空 cookie 应 ErrCookieMissing, got %v", err)
	}
}

func TestParseCookieRoughAndMask(t *testing.T) {
	ok, n, _ := ParseCookieRough("")
	if ok || n != 0 {
		t.Errorf("空串应失败: ok=%v n=%d", ok, n)
	}
	ok, n, _ = ParseCookieRough("nosign")
	if ok || n != 6 {
		t.Errorf("无等号应失败: ok=%v n=%d", ok, n)
	}
	ok, n, prev := ParseCookieRough("dbcl2=abcdefghijklmnop")
	if !ok || n == 0 || !strings.Contains(prev, "…") {
		t.Errorf("合法 cookie: ok=%v n=%d prev=%q", ok, n, prev)
	}
	if MaskCookiePreview("short") != "short" {
		t.Errorf("短串不应截断: %q", MaskCookiePreview("short"))
	}
}

func TestProbeDoubanOnlineURL(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "a=1" {
			t.Errorf("Cookie = %q", r.Header.Get("Cookie"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer okSrv.Close()
	ok, status, _ := ProbeDoubanOnlineURL(context.Background(), okSrv.URL, "a=1", okSrv.Client())
	if !ok || status != "ok" {
		t.Fatalf("期望 ok: ok=%v status=%s", ok, status)
	}

	riskSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("检测到有异常请求"))
	}))
	defer riskSrv.Close()
	ok, status, _ = ProbeDoubanOnlineURL(context.Background(), riskSrv.URL, "a=1", riskSrv.Client())
	if ok || status != "risk" {
		t.Fatalf("期望 risk: ok=%v status=%s", ok, status)
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("no"))
	}))
	defer errSrv.Close()
	ok, status, _ = ProbeDoubanOnlineURL(context.Background(), errSrv.URL, "a=1", errSrv.Client())
	if ok || status != "error" {
		t.Fatalf("期望 error: ok=%v status=%s", ok, status)
	}
}
