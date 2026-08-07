package collector

import (
	"context"
	"os"
	"path/filepath"
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
