package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

// TestConfigTabs 配置二级 Tab：默认 general；仅渲染当前 tab；sources→collector；history 仅 general
func TestConfigTabs(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	if err := store.SetConfig(s, "secret.notifier.pushplus.token", "pp-token-plain"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConfig(s, config.KeyDoubanCookieCloudPwd, "cc-pass-plain"); err != nil {
		t.Fatal(err)
	}

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
	if !strings.Contains(body, "变更历史") {
		t.Errorf("general 应含变更历史")
	}
	if !strings.Contains(body, `name="server.addr"`) {
		t.Errorf("general 应渲染服务配置表单")
	}
	if !strings.Contains(body, `name="server.public_base"`) {
		t.Errorf("general 应渲染对外访问地址")
	}
	if !strings.Contains(body, "修改后需重启") {
		t.Errorf("监听地址等启动钉死项应在标签旁提示需重启")
	}
	if strings.Contains(body, "信息源与 Cookie") || strings.Contains(body, "本配置当前版本仅用于审核帖子") {
		t.Errorf("默认 general 不应渲染其他分区表单")
	}

	code, body = get("/admin/config?tab=sources")
	if code != http.StatusOK {
		t.Fatalf("GET tab=sources status = %d, want 200", code)
	}
	if !strings.Contains(body, `name="section" value="collector"`) {
		t.Errorf("sources tab 表单 section 应为 collector")
	}
	if !strings.Contains(body, "按源切换") {
		t.Errorf("sources 应对齐 collector section")
	}
	if !strings.Contains(body, `value="raw"`) {
		t.Errorf("sources 应含 cookie raw 选项")
	}
	if !strings.Contains(body, "cookie-test-btn") || !strings.Contains(body, "cookiecloud-test-btn") {
		t.Errorf("sources 应含 CookieCloud / Cookie 检测按钮")
	}
	if !strings.Contains(body, "config-modal") {
		t.Errorf("sources 检测结果应走弹窗")
	}
	if !strings.Contains(body, "导出 JSON") || !strings.Contains(body, "/admin/config/export") {
		t.Errorf("配置页应有导出 JSON")
	}
	if !strings.Contains(body, `value="douban"`) || !strings.Contains(body, `value="weibo"`) {
		t.Errorf("sources 启用源应多选 douban/weibo")
	}
	if !strings.Contains(body, `data-source-tab="douban"`) || !strings.Contains(body, `data-source-tab="weibo"`) {
		t.Errorf("sources 应有豆瓣/微博子 tab")
	}
	if strings.Contains(body, `data-source-tab="common"`) {
		t.Errorf("sources 不应再有全局子 tab")
	}
	if !strings.Contains(body, "collector.douban.range_from") {
		t.Errorf("豆瓣 tab 应有拉取起点")
	}
	if strings.Contains(body, "collector.douban.range_to") || strings.Contains(body, "截止（几天后/前）") {
		t.Errorf("截止日期恒为现在，不应再配 range_to")
	}
	if !strings.Contains(body, `value="-10"`) {
		t.Errorf("拉取起点默认应显示 -10")
	}
	if !strings.Contains(body, "起始（几天前）") {
		t.Errorf("豆瓣范围标签应写清起始含义")
	}
	if !strings.Contains(body, "按发布时间筛选") {
		t.Errorf("时间窗应单独成组")
	}
	if !strings.Contains(body, "采集间隔(秒)") || !strings.Contains(body, `name="collector.interval"`) {
		t.Errorf("采集间隔应在各源子页")
	}
	if !strings.Contains(body, `>启用<`) || !strings.Contains(body, `name="collector.sources"`) {
		t.Errorf("各源应有启用勾选")
	}
	if !strings.Contains(body, "请求间隔(秒)") || !strings.Contains(body, `name="collector.douban.interval"`) {
		t.Errorf("豆瓣请求间隔应单独展示")
	}
	if !strings.Contains(body, "检测 Cookie") || !strings.Contains(body, "检测 CookieCloud") {
		t.Errorf("Cookie 检测应放在 Cookie 配置块内")
	}
	if strings.Contains(body, `type="password" name="secret.collector.douban.cookiecloud_password"`) {
		t.Errorf("CookieCloud 密码应为明文 text")
	}
	if !strings.Contains(body, "cc-pass-plain") {
		t.Errorf("CookieCloud 密码应明文回显")
	}
	if !strings.Contains(body, "采集暂未实现") {
		t.Errorf("微博子 tab 应有暂未实现提示")
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
	if !strings.Contains(body, "log.memory_lines") || !strings.Contains(body, "占内存") {
		t.Errorf("general 应有内存日志条数及内存占用提示")
	}
	if !strings.Contains(body, `name="log.level"`) || !strings.Contains(body, `value="debug"`) || !strings.Contains(body, `value="warn"`) {
		t.Errorf("日志级别应为 debug/info/warn/error 下拉")
	}
	if !strings.Contains(body, "/admin/config/history?id=") || !strings.Contains(body, ">详情<") {
		t.Errorf("变更历史应有详情按钮")
	}
	if strings.Contains(body, ">显示<") {
		t.Errorf("变更历史不应再用「显示」按钮")
	}

	code, body = get("/admin/config?tab=ai")
	if code != http.StatusOK {
		t.Fatalf("GET tab=ai status = %d", code)
	}
	if !strings.Contains(body, "本配置当前版本仅用于审核帖子") {
		t.Errorf("ai tab Desc 不符")
	}
	if !strings.Contains(body, "filter.batch_size") || !strings.Contains(body, "filter.ai_batch_size") {
		t.Errorf("ai 应含组批/AI 批（高级配置）")
	}
	if !strings.Contains(body, "高级配置") {
		t.Errorf("组批/AI 批应折在高级配置里")
	}
	if strings.Contains(body, "积压够了就开干") {
		t.Errorf("主表单不应再露出组批运营文案")
	}
	if strings.Contains(body, "豆瓣截断") || strings.Contains(body, "trim_limits") {
		t.Errorf("ai 不应含 trim_limits")
	}
	if !strings.Contains(body, "secret.filter.llm.api_style") || !strings.Contains(body, "LLM 提供方") {
		t.Errorf("ai tab 应渲染 AI 审核主表单")
	}
	if !strings.Contains(body, `name="filter.ai_enabled"`) {
		t.Errorf("ai 应有启用勾选")
	}
	if !strings.Contains(body, "llm-test-btn") {
		t.Errorf("ai 应有连通检测按钮")
	}
	if !strings.Contains(body, "config-modal") {
		t.Errorf("ai 连通检测结果应走弹窗")
	}
	if !strings.Contains(body, "llm-fetch-models-btn") || !strings.Contains(body, "llm-model-select") {
		t.Errorf("ai 主模型应为下拉+拉取按钮")
	}
	if strings.Contains(body, "fallback_models") {
		t.Errorf("ai 不应含 fallback")
	}
	if !strings.Contains(body, "https://api.deepseek.com") {
		t.Errorf("ai Base URL 默认 deepseek")
	}
	if strings.Contains(body, "启用 AI") {
		t.Errorf("ai 不应再有单独「启用 AI」checkbox")
	}

	// 旧 tab=filter 兼容映射到 ai
	code, body = get("/admin/config?tab=filter")
	if code != http.StatusOK {
		t.Fatalf("GET tab=filter status = %d", code)
	}
	if !strings.Contains(body, "本配置当前版本仅用于审核帖子") {
		t.Errorf("tab=filter 应兼容渲染 AI 分区")
	}

	code, body = get("/admin/config?tab=notifier")
	if code != http.StatusOK {
		t.Fatalf("GET tab=notifier status = %d", code)
	}
	if !strings.Contains(body, "PushPlus") || !strings.Contains(body, "secret.notifier.pushplus.token") || !strings.Contains(body, "secret.notifier.pushplus.topic") {
		t.Errorf("notifier 应含飞书/PushPlus 子 tab")
	}
	if strings.Contains(body, `data-notify-tab="common"`) {
		t.Errorf("notifier 不应再有通用子 tab")
	}
	if !strings.Contains(body, "notifier.batch_size") {
		t.Errorf("notifier 各渠道应含组批大小")
	}
	if !strings.Contains(body, "两者满足其一即执行发送") && !strings.Contains(body, "满足其一即执行发送") {
		t.Errorf("重试间隔/组批大小应提示二者满足其一即发送")
	}
	if strings.Contains(body, `type="password" name="secret.notifier.pushplus.token"`) {
		t.Errorf("PushPlus Token 应为明文 text")
	}
	if !strings.Contains(body, "pp-token-plain") {
		t.Errorf("PushPlus Token 应明文回显")
	}
	if !strings.Contains(body, `name="notifier.channels"`) || !strings.Contains(body, `>启用<`) {
		t.Errorf("各通知渠道应有独立启用勾选")
	}
	if strings.Contains(body, "secret.notifier.serverchan") {
		t.Errorf("notifier UI 本期不应露出 Server酱")
	}
	if strings.Count(body, "检测连通性") < 2 {
		t.Errorf("飞书和 PushPlus 都应有检测连通性")
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
	for _, k := range []string{"server.addr", "log.path", "log.level"} {
		if !RestartKeys[k] {
			t.Errorf("RestartKeys 缺 %q", k)
		}
	}
	for _, k := range []string{
		"server.public_base", "log.memory_lines", "collector.interval",
		"collector.sources", "collector.douban.groups",
		"filter.ai_enabled", "secret.filter.llm.api_key",
		"notifier.channels", "secret.notifier.feishu.webhook",
	} {
		if RestartKeys[k] {
			t.Errorf("%s 应热生效，不应列入 RestartKeys", k)
		}
	}
}

// TestChangedRestartKeys 仅报告实际变更的需重启项
func TestChangedRestartKeys(t *testing.T) {
	before := map[string]string{"server.addr": ":7777", "log.level": "info", "admin.token": "a"}
	updates := map[string]string{"server.addr": ":8888", "log.level": "debug", "admin.token": "b"}
	got := changedRestartKeys(before, updates)
	if len(got) != 2 || got[0] != "log.level" || got[1] != "server.addr" {
		t.Errorf("changedRestartKeys = %v, want [log.level server.addr]", got)
	}
}

func TestConfigExportJSON(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/config/export", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "rent-scout-config.json") {
		t.Errorf("缺附件文件名: %s", rec.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(rec.Body.String(), `"collector.douban.range_from"`) {
		t.Errorf("导出 JSON 应含配置 key: %s", rec.Body.String())
	}
}

func TestConfigHistorySnapshotPage(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)

	if err := store.SetConfig(s, "log.level", "debug"); err != nil {
		t.Fatal(err)
	}
	hist, err := store.ListConfigHistory(s, 50)
	if err != nil || len(hist) == 0 {
		t.Fatalf("history: n=%d err=%v", len(hist), err)
	}
	var id int64
	for _, e := range hist {
		if e.Key == "log.level" && e.NewValue == "debug" {
			id = e.ID
			break
		}
	}
	if id == 0 {
		t.Fatal("没找到 log.level=debug 的历史")
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/config/history?id="+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "只读快照") || !strings.Contains(body, "disabled") {
		t.Errorf("历史页应只读")
	}
	if !strings.Contains(body, `value="debug"`) {
		t.Errorf("快照应显示当时 log.level=debug")
	}
	if strings.Contains(body, "保存「") {
		t.Errorf("历史页不应有保存")
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/config/history?id=999999", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知 id status = %d, want 404", rec.Code)
	}
}
