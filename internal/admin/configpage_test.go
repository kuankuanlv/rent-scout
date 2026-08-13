package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

// TestConfigTabs 配置二级 Tab：默认 general；仅渲染当前 tab；sources→collector；history 仅 general
func TestConfigTabs(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, body := get("/admin/config")
	if code != http.StatusOK {
		t.Fatalf("GET /admin/config status = %d, want 200", code)
	}
	for _, want := range []string{"tab=general", "tab=sources", "tab=rules", "tab=filter", "tab=notifier", "tab=admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("缺二级 Tab 链接 %q", want)
		}
	}
	if !strings.Contains(body, "服务运行基础参数") {
		t.Errorf("默认 tab 应渲染 general")
	}
	if !strings.Contains(body, "变更历史（只读）") {
		t.Errorf("general 应含变更历史")
	}
	if strings.Contains(body, "信息源与 Cookie") || strings.Contains(body, "AI 与 LLM") {
		t.Errorf("默认 general 不应渲染其他分区表单")
	}

	code, body = get("/admin/config?tab=sources")
	if code != http.StatusOK {
		t.Fatalf("GET tab=sources status = %d, want 200", code)
	}
	if !strings.Contains(body, `name="section" value="collector"`) {
		t.Errorf("sources tab 表单 section 应为 collector")
	}
	if !strings.Contains(body, "信息源与 Cookie") {
		t.Errorf("sources 应对齐 collector section")
	}
	if !strings.Contains(body, `value="raw"`) {
		t.Errorf("sources 应含 cookie raw 选项")
	}
	if !strings.Contains(body, "cookie-test-btn") {
		t.Errorf("sources 应含 Cookie 测试按钮")
	}
	if strings.Contains(body, "变更历史（只读）") {
		t.Errorf("非 general 不应含变更历史")
	}
	if strings.Contains(body, "服务运行基础参数") {
		t.Errorf("sources 不应渲染 general")
	}

	code, body = get("/admin/config?tab=rules")
	if code != http.StatusOK {
		t.Fatalf("GET tab=rules status = %d, want 200", code)
	}
	if !strings.Contains(body, "规则管理") {
		t.Errorf("rules tab 应渲染规则面板")
	}
	if strings.Contains(body, `name="section"`) {
		t.Errorf("rules tab 不应渲染配置分区表单")
	}
}

// TestConfigSectionSaveRestartBanner 改需重启项 → Location 带 restart=1；admin.token 不带
func TestConfigSectionSaveRestartBanner(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)

	post := func(form url.Values) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/config/save", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST save status = %d, want 302; body=%s", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("Location")
	}

	// server.addr 在 RestartKeys → 应提示重启
	loc := post(url.Values{
		"section":                  {"general"},
		"server.addr":              {":9999"},
		"log.level":                {"info"},
		"log.path":                 {""},
		"pipeline.batch_size":      {"20"},
		"pipeline.linger_interval": {"30"},
	})
	if !strings.Contains(loc, "restart=1") || !strings.Contains(loc, "ok=general") {
		t.Errorf("改 server.addr Location = %q, want restart=1", loc)
	}

	// 黄条渲染
	req := httptest.NewRequest(http.MethodGet, "/admin/config?tab=general&ok=general&restart=1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET restart banner status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "部分配置需重启服务后生效") {
		t.Errorf("应渲染黄条")
	}

	// admin.token 热生效 → 无 restart
	loc = post(url.Values{
		"section":             {"admin"},
		"admin.auth_required": {"on"},
		"admin.token":         {"new-token-xyz"},
	})
	if strings.Contains(loc, "restart=") {
		t.Errorf("改 admin.token 不应带 restart: %q", loc)
	}
	if !strings.Contains(loc, "ok=admin") {
		t.Errorf("Location = %q, want ok=admin", loc)
	}
}

// TestRestartKeysExcludesAdminToken admin.token / auth_required 不在需重启集合
func TestRestartKeysExcludesAdminToken(t *testing.T) {
	if RestartKeys["admin.token"] || RestartKeys["admin.auth_required"] {
		t.Fatal("admin.token / auth_required 应热生效，不得列入 RestartKeys")
	}
	for _, k := range []string{"server.addr", "log.path", "collector.sources", "secret.filter.llm.api_key", "secret.notifier.feishu.webhook"} {
		if !RestartKeys[k] {
			t.Errorf("RestartKeys 缺 %q", k)
		}
	}
}

// TestChangedRestartKeys 仅报告实际变更的需重启项
func TestChangedRestartKeys(t *testing.T) {
	before := map[string]string{"server.addr": ":7777", "log.level": "info", "admin.token": "a"}
	updates := map[string]string{"server.addr": ":8888", "log.level": "debug", "admin.token": "b"}
	got := changedRestartKeys(before, updates)
	if len(got) != 1 || got[0] != "server.addr" {
		t.Errorf("changedRestartKeys = %v, want [server.addr]", got)
	}
}
