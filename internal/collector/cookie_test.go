package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

// file 模式：读本地 cookie 文件（规格 4.4）
func TestFileCookieProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "douban.txt")
	if err := os.WriteFile(path, []byte("cookie-a=1; cookie-b=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := NewCookieProvider("file", path, config.DoubanCookieConfig{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "douban")
	if err != nil {
		t.Fatal(err)
	}
	if got != "cookie-a=1; cookie-b=2" {
		t.Errorf("Get = %q", got)
	}
}

// none 模式：匿名（返回空串）
func TestNoneCookieProvider(t *testing.T) {
	p, err := NewCookieProvider("none", "", config.DoubanCookieConfig{})
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
	p, err := NewCookieProvider("raw", "", config.DoubanCookieConfig{CookieRaw: " dbcl2=abc; bid=xyz "})
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

// Runtime provider：每次 Get 读最新 Env
func TestRuntimeCookieProviderFollowsEnv(t *testing.T) {
	env := &config.EnvLocalConfig{
		Collector: config.EnvCollector{
			Douban: config.DoubanCookieConfig{CookieMode: "raw", CookieRaw: "a=1"},
		},
	}
	rt := config.NewRuntimeWithSnapshot(nil, env)
	p := NewRuntimeCookieProvider(rt)
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

// file 模式文件缺失：降级匿名 + 不报错（规格 4.4 读取失败降级）
func TestFileCookieProviderMissing(t *testing.T) {
	p, err := NewCookieProvider("file", filepath.Join(t.TempDir(), "nope.txt"), config.DoubanCookieConfig{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "douban")
	if err != nil {
		t.Fatalf("缺失文件应降级匿名: %v", err)
	}
	if got != "" {
		t.Errorf("缺失文件应返回空: %q", got)
	}
}

// 未知模式：报错（配置错误应显式暴露）
func TestUnknownCookieMode(t *testing.T) {
	if _, err := NewCookieProvider("weird", "", config.DoubanCookieConfig{}); err == nil {
		t.Fatal("未知模式应报错")
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
