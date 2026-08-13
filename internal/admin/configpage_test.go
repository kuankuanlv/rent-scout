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
	for _, want := range []string{"tab=general", "tab=sources", "tab=rules", "tab=ai", "tab=notifier", "tab=admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("缺二级 Tab 链接 %q", want)
		}
	}
	if strings.Contains(body, "tab=filter\"") || strings.Contains(body, ">筛选<") {
		t.Errorf("顶栏不应再露出旧 filter/筛选 tab")
	}
	if !strings.Contains(body, "服务运行基础参数") {
		t.Errorf("默认 tab 应渲染 general")
	}
	if !strings.Contains(body, "变更历史（只读）") {
		t.Errorf("general 应含变更历史")
	}
	if strings.Contains(body, "信息源与 Cookie") || strings.Contains(body, "本配置用于审核帖子") {
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
	if !strings.Contains(body, "cookie-test-summary") {
		t.Errorf("sources Cookie 结果区应始终可见")
	}
	if !strings.Contains(body, `value="douban"`) || !strings.Contains(body, `value="weibo"`) {
		t.Errorf("sources 启用源应多选 douban/weibo")
	}
	if !strings.Contains(body, `data-source-tab="douban"`) || !strings.Contains(body, `data-source-tab="weibo"`) {
		t.Errorf("sources 应有豆瓣/微博子 tab")
	}
	if !strings.Contains(body, "collector.douban.range_from") || !strings.Contains(body, "collector.douban.range_to") {
		t.Errorf("豆瓣 tab 应有拉取范围从/到")
	}
	if !strings.Contains(body, `value="-10d"`) || !strings.Contains(body, `value="now"`) {
		t.Errorf("拉取范围默认应显示 -10d 与 now")
	}
	if !strings.Contains(body, "暂未实现") {
		t.Errorf("微博子 tab 应有占位文案")
	}
	if strings.Contains(body, "豆瓣截断") || strings.Contains(body, "trim_limits") {
		t.Errorf("不应再出现 trim_limits UI")
	}
	if strings.Contains(body, "变更历史（只读）") {
		t.Errorf("非 general 不应含变更历史")
	}
	if strings.Contains(body, "服务运行基础参数") {
		t.Errorf("sources 不应渲染 general")
	}

	code, body = get("/admin/config?tab=general")
	if code != http.StatusOK {
		t.Fatalf("GET tab=general status = %d", code)
	}
	if strings.Contains(body, "pipeline.batch_size") || strings.Contains(body, "pipeline.linger_interval") {
		t.Errorf("general 不应再含组批/兜底字段")
	}
	if strings.Contains(body, "积压够了就开干") || strings.Contains(body, "最多等多久也强制跑一轮") {
		t.Errorf("general 不应再有组批/兜底 tip")
	}

	code, body = get("/admin/config?tab=ai")
	if code != http.StatusOK {
		t.Fatalf("GET tab=ai status = %d", code)
	}
	if !strings.Contains(body, "本配置用于审核帖子，并会记录审核通过/拒绝的具体原因") {
		t.Errorf("ai tab Desc 不符")
	}
	if !strings.Contains(body, "filter.batch_size") || !strings.Contains(body, "积压够了就开干") {
		t.Errorf("ai 应含 filter 组批大小")
	}
	if strings.Contains(body, "豆瓣截断") || strings.Contains(body, "trim_limits") {
		t.Errorf("ai 不应含 trim_limits")
	}
	if !strings.Contains(body, "secret.filter.llm.api_style") || !strings.Contains(body, "llm-test-btn") {
		t.Errorf("ai 应含 API 风格与连通检测")
	}
	if !strings.Contains(body, `value="none"`) || !strings.Contains(body, ">无<") {
		t.Errorf("ai 应含「无」选项")
	}
	if !strings.Contains(body, `value="openai"`) || !strings.Contains(body, ">OpenAI<") {
		t.Errorf("ai 应含 OpenAI 选项")
	}
	if !strings.Contains(body, `value="other"`) || !strings.Contains(body, ">其他<") {
		t.Errorf("ai 应含「其他」选项")
	}
	if strings.Contains(body, `value="custom"`) {
		t.Errorf("ai 不应再露出 custom 选项")
	}
	if strings.Contains(body, "启用 AI") {
		t.Errorf("ai 不应再有单独「启用 AI」checkbox")
	}

	// 旧 tab=filter 兼容映射到 ai
	code, body = get("/admin/config?tab=filter")
	if code != http.StatusOK {
		t.Fatalf("GET tab=filter status = %d", code)
	}
	if !strings.Contains(body, "本配置用于审核帖子") {
		t.Errorf("tab=filter 应兼容渲染 AI 分区")
	}

	code, body = get("/admin/config?tab=notifier")
	if code != http.StatusOK {
		t.Fatalf("GET tab=notifier status = %d", code)
	}
	if !strings.Contains(body, "PushPlus") || !strings.Contains(body, "secret.notifier.pushplus.token") {
		t.Errorf("notifier 应含飞书/PushPlus 子 tab")
	}
	if !strings.Contains(body, "notifier.batch_size") {
		t.Errorf("notifier common 应含组批大小")
	}
	if strings.Contains(body, "secret.notifier.serverchan") {
		t.Errorf("notifier UI 本期不应露出 Server酱")
	}

	code, body = get("/admin/config?tab=rules")
	if code != http.StatusOK {
		t.Fatalf("GET tab=rules status = %d, want 200", code)
	}
	if !strings.Contains(body, "规则管理") {
		t.Errorf("rules tab 应渲染规则面板")
	}
	if !strings.Contains(body, `value="50"`) || !strings.Contains(body, "数字越大越先执行") {
		t.Errorf("规则新增区默认优先级 50 + tip")
	}
	if !strings.Contains(body, ">黑名单<") || !strings.Contains(body, ">白名单<") || !strings.Contains(body, ">AI审核<") {
		t.Errorf("规则类型下拉应为黑名单/白名单/AI审核")
	}
	if strings.Contains(body, ">保存<") || strings.Contains(body, ">保存</button>") {
		t.Errorf("规则行不应再有保存按钮")
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
		"section":     {"general"},
		"server.addr": {":9999"},
		"log.level":   {"info"},
		"log.path":    {""},
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
