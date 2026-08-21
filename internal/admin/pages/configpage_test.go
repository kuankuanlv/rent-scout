package pages_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"rent-scout/internal/admin/pages"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/admin/testutil"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/store"
	"strconv"
	"strings"
	"testing"
)

// TestConfigTabs 配置二级 Tab：默认 general；仅渲染当前 tab；sources→collector；history 仅 general
func TestConfigTabs(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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
	if !strings.Contains(body, "默认都不开") {
		t.Errorf("sources 应对齐 collector section，并说明默认关闭")
	}
	if !strings.Contains(body, "还没开始采集") {
		t.Errorf("sources 首次应提示去启用采集源")
	}
	if !strings.Contains(body, "把 Cookie 整段贴进来即可") {
		t.Errorf("Cookie 原文应提示直接粘贴")
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
	if !strings.Contains(body, "/admin/config/import") || !strings.Contains(body, "确认导入") {
		t.Errorf("配置页应有导入")
	}
	// 回归：导入下拉面板若嵌套在 overflow-x-auto 容器内会被裁剪不可见（overflow-y 被计算为 auto）。
	// 横向滚动容器应只包裹 Tab 区（flex-1），不能是包住导出/导入按钮的整条顶栏。
	if i := strings.Index(body, "overflow-x-auto"); i >= 0 {
		open := body[strings.LastIndex(body[:i], "<div"):i]
		if !strings.Contains(open, "flex-1") {
			t.Errorf("overflow-x-auto 应只包裹 Tab 区（flex-1），否则导入下拉面板会被裁剪不可见")
		}
	}
	if !strings.Contains(body, `value="douban"`) || !strings.Contains(body, `value="weibo"`) {
		t.Errorf("sources 启用源应多选 douban/weibo")
	}
	if !strings.Contains(body, `data-source-tab="douban"`) || !strings.Contains(body, `data-source-tab="weibo"`) {
		t.Errorf("sources 应有豆瓣/微博子 tab")
	}
	if !strings.Contains(body, `name="collector.weibo.range_from"`) {
		t.Errorf("微博 tab 应有发布时间筛选")
	}
	if strings.Contains(body, `name="collector.max_age_days"`) {
		t.Errorf("微博/豆瓣不应再露出 max_age_days")
	}
	if !strings.Contains(body, `name="secret.collector.weibo.cookie_mode"`) {
		t.Errorf("微博 tab 应有 cookie 配置")
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
	if !strings.Contains(body, `type="password" name="secret.collector.douban.cookiecloud_password"`) {
		t.Errorf("CookieCloud 密码应按 password 掩码回显")
	}
	if !strings.Contains(body, "cc-pass-plain") {
		t.Errorf("CookieCloud 密码应明文回显")
	}
	if !strings.Contains(body, "超话和租房博主") {
		t.Errorf("微博子 tab 应说明超话与博主采集")
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
		t.Errorf("ai tab Desc 应说明仅用于审核")
	}
	if !strings.Contains(body, "系统提示词") || !strings.Contains(body, "verdicts") || !strings.Contains(body, "500 字") {
		t.Errorf("ai tab Desc 应说明 prompt、结构化输出和省 token")
	}
	if !strings.Contains(body, "filter.ai_batch_size") {
		t.Errorf("ai 应含 AI 批大小（高级配置）")
	}
	if strings.Contains(body, "filter.batch_size") {
		t.Errorf("ai 不应再含组批大小（hard 不等批，无需配置）")
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
	if !strings.Contains(body, "api.deepseek.com") {
		t.Errorf("ai Base URL 默认 DeepSeek")
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
	if !strings.Contains(body, "notifier.interval") {
		t.Errorf("notifier 各渠道应含发送间隔")
	}
	if !strings.Contains(body, "谁先到谁先发") {
		t.Errorf("组批大小应提示和发送间隔谁先到谁先发")
	}
	if strings.Contains(body, "notifier.max_attempts") || strings.Contains(body, "notifier.retry_base_interval") {
		t.Errorf("重试次数/间隔不应出现在配置页")
	}
	if !strings.Contains(body, `type="password" name="secret.notifier.pushplus.token"`) {
		t.Errorf("PushPlus Token 应按 password 掩码回显")
	}
	if !strings.Contains(body, "pp-token-plain") {
		t.Errorf("PushPlus Token 应回显已保存值")
	}
	if !strings.Contains(body, `class="pw-toggle`) {
		t.Errorf("密码字段应提供「显示明文」按钮")
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
	s := testutil.NewAdminTestStore(t)
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

	// server.addr 在 pages.RestartKeys → 应提示重启
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
	if pages.RestartKeys["admin.token"] || pages.RestartKeys["admin.auth_required"] {
		t.Fatal("admin.token / auth_required 应热生效，不得列入 pages.RestartKeys")
	}
	for _, k := range []string{"server.addr", "log.path", "log.level"} {
		if !pages.RestartKeys[k] {
			t.Errorf("pages.RestartKeys 缺 %q", k)
		}
	}
	for _, k := range []string{
		"server.public_base", "log.memory_lines", "collector.interval",
		"collector.sources", "collector.douban.groups",
		"filter.ai_enabled", "secret.filter.llm.api_key",
		"notifier.channels", "secret.notifier.feishu.webhook",
	} {
		if pages.RestartKeys[k] {
			t.Errorf("%s 应热生效，不应列入 pages.RestartKeys", k)
		}
	}
}

// TestChangedRestartKeys 仅报告实际变更的需重启项
func TestChangedRestartKeys(t *testing.T) {
	before := map[string]string{"server.addr": ":7777", "log.level": "info", "admin.token": "a"}
	updates := map[string]string{"server.addr": ":8888", "log.level": "debug", "admin.token": "b"}
	got := pages.ChangedRestartKeys(before, updates)
	if len(got) != 2 || got[0] != "log.level" || got[1] != "server.addr" {
		t.Errorf("pages.ChangedRestartKeys = %v, want [log.level server.addr]", got)
	}
}

func TestConfigExportJSON(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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

func TestConfigImportLines(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)

	form := url.Values{"data": {"server.addr=:9191\nlog.level=warn"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if v, _ := store.GetConfig(s, "server.addr"); v != ":9191" {
		t.Errorf("server.addr = %q", v)
	}
	if v, _ := store.GetConfig(s, "log.level"); v != "warn" {
		t.Errorf("log.level = %q", v)
	}
}

func TestConfigImportJSONRoundTrip(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	app := config.DefaultApp()
	app.Log.Level = "debug"
	srv := newTestServerWithStore(t, s, app, "", nil)

	exp := httptest.NewRequest(http.MethodGet, "/admin/config/export", nil)
	expRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(expRec, exp)
	if expRec.Code != http.StatusOK {
		t.Fatal(expRec.Code)
	}
	if err := store.SetConfig(s, "log.level", "info"); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"data": {expRec.Body.String()}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if v, _ := store.GetConfig(s, "log.level"); v != "debug" {
		t.Errorf("log.level = %q, want debug", v)
	}
}

func TestConfigHistorySnapshotPage(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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

func TestParseSectionFormGroupKeepsOtherSource(t *testing.T) {
	keep := map[string]string{
		"collector.sources":       "douban,weibo",
		"collector.weibo.users":   "6342026928",
		"collector.douban.groups": "https://www.douban.com/group/1/",
	}
	form := url.Values{
		"section":                {"collector"},
		"group":                  {"weibo"},
		"collector.sources":      {"weibo"},
		"collector.weibo.users":  {"1111111111"},
		"collector.interval":     {"300"},
		"collector.jitter_ratio": {"0.2"},
	}
	got := pages.ParseSectionForm(form, "collector", keep)
	if got["collector.weibo.users"] != "1111111111" {
		t.Errorf("weibo users = %q", got["collector.weibo.users"])
	}
	if _, ok := got["collector.douban.groups"]; ok {
		t.Errorf("不应提交豆瓣字段: %v", got)
	}
	if got["collector.sources"] != "douban,weibo" {
		t.Errorf("sources merge = %q, want douban,weibo", got["collector.sources"])
	}
}

func TestConfigSectionSaveJSONNoRedirect(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	form := url.Values{
		"section":     {"general"},
		"server.addr": {":7777"},
		"log.level":   {"info"},
		"log.path":    {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("JSON 保存不应 302: %s", loc)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Errorf("body=%v", out)
	}
}

func TestCookieTestNoneProbesOnline(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) ports.DoubanPageResult {
		if c != "" {
			t.Errorf("匿名探测不应带 cookie, got len=%d", len(c))
		}
		return ports.DoubanPageResult{OK: false, HTTP: 200, Snippet: "有异常请求从你的 IP 发出，请 登录 使用豆瓣"}
	}})

	form := url.Values{"cookie_mode": {"none"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("无 cookie 风控应失败, ok=%v", out["ok"])
	}
	if out["http"] != float64(200) {
		t.Errorf("http = %v, want 200", out["http"])
	}
	snip, _ := out["snippet"].(string)
	if !strings.Contains(snip, "请 登录") && !strings.Contains(snip, "异常请求") {
		t.Errorf("snippet 应含豆瓣原文: %q", snip)
	}
}

func TestCookieTestRawDraftNoWrite(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) ports.DoubanPageResult {
		return ports.DoubanPageResult{OK: false, HTTP: 403, Snippet: "forbidden"}
	}})

	before, _ := store.GetConfigMap(s)
	form := url.Values{
		"cookie_mode": {"raw"},
		"cookie_raw":  {"bid=abc"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("探测失败应 ok=false, got %v", out["ok"])
	}
	if out["http"] != float64(403) {
		t.Errorf("http = %v, want 403", out["http"])
	}
	after, _ := store.GetConfigMap(s)
	if after["secret.collector.douban.cookie_raw"] != before["secret.collector.douban.cookie_raw"] {
		t.Error("test 不应写库 cookie_raw")
	}
}

func TestCookieTestWeiboProbesWeiboURL(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) ports.DoubanPageResult {
		if !strings.Contains(rawURL, "weibo.com") {
			t.Errorf("微博探测 URL = %q", rawURL)
		}
		if c != "SUB=wb" {
			t.Errorf("cookie = %q", c)
		}
		return ports.DoubanPageResult{OK: true, HTTP: 200}
	}})
	form := url.Values{
		"source":                {"weibo"},
		"cookie_mode":           {"raw"},
		"cookie_raw":            {"SUB=wb"},
		"collector.weibo.users": {"6342026928"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("body=%s", rec.Body.String())
	}
}

func TestCookieTestRawEmptyDoesNotUseStored(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	srv.SetCookieProbe(stubPageProbe{fn: func(ctx context.Context, rawURL, c string) ports.DoubanPageResult {
		if c != "" {
			t.Errorf("空草稿不应回落到库里的 cookie, got %q", c)
		}
		return ports.DoubanPageResult{OK: true, HTTP: 200}
	}})
	if err := store.SetConfigBatch(s, map[string]string{
		"secret.collector.douban.cookie_mode": "raw",
		"secret.collector.douban.cookie_raw":  "dbcl2=storedvalue123456",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.ReloadConfig(); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"cookie_mode": {"raw"}, "cookie_raw": {""}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("页面未填 cookie 应失败: %v", out)
	}
}

func TestCookieTestMethodNotAllowed(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/config/cookie/test", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestCookieTestInvalidModeRejected(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{"cookie_mode": {"file"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookie/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("无效 mode 应失败: %v", out)
	}
}

func TestCookieCloudTestIncomplete(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{"cookie_mode": {"cookiecloud"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookiecloud/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("缺 url/key 应失败: %v", out)
	}
	sum, _ := out["summary"].(string)
	if !strings.Contains(sum, "页面上的 CookieCloud") {
		t.Errorf("summary 应要求填写页面三元组: %q", sum)
	}
}

func TestCookieCloudTestOK(t *testing.T) {
	cc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cookie_data":{"www.douban.com":[{"name":"bid","value":"abc123","domain":".douban.com"}]}}`))
	}))
	defer cc.Close()

	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{
		"cookie_mode":          {"cookiecloud"},
		"cookiecloud_url":      {cc.URL},
		"cookiecloud_key":      {"uuid-1"},
		"cookiecloud_password": {"pass"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookiecloud/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("应通过: %v", out)
	}
	if out["summary"] != "通过" {
		t.Errorf("summary = %v", out["summary"])
	}
	names, _ := out["cookie_names"].([]any)
	if len(names) == 0 {
		t.Errorf("应列出 cookie 名: %v", out)
	}
}

func TestCookieHeaderPreviews(t *testing.T) {
	got := cookie.CookieHeaderPreviews("dbcl2=abcdefghijklmnop; bid=xyz")
	if len(got) != 2 {
		t.Fatalf("previews=%v", got)
	}
	if strings.Contains(got[0], "abcdefghijklmnop") {
		t.Errorf("不应含明文: %v", got)
	}
}

func TestCookieCloudTestUsesPageTripleAndSource(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	probe := &recCloudProbe{}
	srv.SetCookieProbe(probe)
	if err := store.SetConfigBatch(s, map[string]string{
		"secret.collector.douban.cookiecloud_url":      "https://stored-douban.example",
		"secret.collector.douban.cookiecloud_key":      "db-uuid",
		"secret.collector.douban.cookiecloud_password": "db-pass",
		"secret.collector.weibo.cookiecloud_url":       "https://stored-weibo.example",
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"source":               {"weibo"},
		"cookie_mode":          {"cookiecloud"},
		"cookiecloud_url":      {"https://page-weibo.example"},
		"cookiecloud_key":      {"page-uuid"},
		"cookiecloud_password": {"page-pass"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/cookiecloud/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if probe.source != "weibo" {
		t.Errorf("source = %q, want weibo", probe.source)
	}
	if probe.url != "https://page-weibo.example" {
		t.Errorf("url = %q, 应用页面草稿而不是库里的豆瓣/微博", probe.url)
	}
}

type recCloudProbe struct {
	source string
	url    string
}

func (p *recCloudProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (ports.CookieCloudInspect, error) {
	p.source = source
	p.url = draft.CookiecloudURL
	return ports.CookieCloudInspect{Cookie: "SUB=1", HTTPStatus: 200}, nil
}

func (p *recCloudProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) ports.DoubanPageResult {
	return ports.DoubanPageResult{OK: true, HTTP: 200}
}

type stubPageProbe struct {
	fn func(ctx context.Context, probeURL, rawCookie string) ports.DoubanPageResult
}

func (s stubPageProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (ports.CookieCloudInspect, error) {
	return testutil.TestCookieProbe{}.InspectCookieCloud(ctx, draft, source)
}

func (s stubPageProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) ports.DoubanPageResult {
	return s.fn(ctx, probeURL, rawCookie)
}
