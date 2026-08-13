package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

// TestRulesPage GET /admin/rules → 302 到配置 tab=rules；配置页含规则名与命中统计
func TestRulesPage(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	// 播种规则
	r1, err := s.CreateRule(models.Rule{Name: "黑中介", Type: models.RuleTypeBlacklist, Value: "中介", Enabled: true, Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(models.Rule{Name: "地铁近", Type: models.RuleTypeWhitelist, Value: "地铁", Enabled: true, Priority: 1}); err != nil {
		t.Fatal(err)
	}

	// 播种命中数据：passed 帖硬规则命中 r1，且被标 useless → r1 {Hits:1, UselessCount:1}
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "hit1", Title: "命中帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	pid := postID(t, s, "hit1")
	if err := s.SaveFilterResult(models.FilterResult{PostID: pid, Status: models.PostStatusPassed, Stage: models.StageHardRule,
		DecidedAt: time.Now(), HardRules: []models.RuleHit{{RuleID: r1.ID, Reason: "中介"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFeedback(models.Feedback{PostID: pid, Action: models.FeedbackUseless, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/rules", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /admin/rules status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/config?tab=rules" {
		t.Fatalf("Location = %q, want /admin/config?tab=rules", loc)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/admin/config?tab=rules", nil)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /admin/config?tab=rules status = %d, want 200", rec2.Code)
	}
	body := rec2.Body.String()
	for _, want := range []string{"规则管理", "黑中介", "地铁近", "白名单 → 黑名单 → AI", "「或」", "Tag",
		`value="whitelist"`, `value="blacklist"`, `value="ai_natural"`, "白名单", "黑名单", "AI审核"} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q", want)
		}
	}
	for _, bad := range []string{"多条规则之间是且", "规则之间是且", "hard_keyword", "hard_blacklist", "hard_whitelist", `name="mode"`} {
		if strings.Contains(body, bad) {
			t.Errorf("页面不应含 %q", bad)
		}
	}
	// 命中统计列：r1 行（Hits=1, UselessCount=1，紧随启用 checkbox 单元格）
	if !strings.Contains(body, `text-center">1</td><td class="px-3 py-2 text-center">1</td>`) {
		t.Errorf("页面缺 r1 命中统计（Hits=1/标无用=1）")
	}
}

// TestRulesCreate POST /admin/rules 合法 → 302（PRG）+ DB 有记录；Location 回列表
func TestRulesCreate(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{
		"name":     {"留学生优先"},
		"type":     {models.RuleTypeBlacklist},
		"value":    {"押一付一"},
		"priority": {"5"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/rules status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/config?tab=rules" {
		t.Errorf("Location = %q, want /admin/config?tab=rules", loc)
	}
	rules, err := s.ListRules(false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rules {
		if r.Name == "留学生优先" && r.Type == models.RuleTypeBlacklist &&
			r.Value == "押一付一" && r.Enabled && r.Priority == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("DB 无新增规则: %+v", rules)
	}
}

// TestRulesUpdate POST /admin/rules/{id} 改 value + 关启用 → 生效（另有启用规则，不触底）
func TestRulesUpdate(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "改前", Type: models.RuleTypeBlacklist, Value: "旧值", Enabled: true, Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(models.Rule{Name: "保底", Type: models.RuleTypeWhitelist, Value: "望京", Enabled: true, Priority: 1}); err != nil {
		t.Fatal(err)
	}

	// enabled 不提交 = checkbox 未勾选 → 关启用
	form := url.Values{
		"name":     {"改前"},
		"type":     {r.Type},
		"value":    {"新值,代理"},
		"priority": {"8"},
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d", r.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/rules/%d status = %d, want 302", r.ID, rec.Code)
	}
	rules, err := s.ListRules(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rules {
		if g.ID == r.ID {
			if g.Value != "新值,代理" || g.Priority != 8 || g.Enabled {
				t.Errorf("更新未生效: %+v, want value=新值,代理 priority=8 enabled=false", g)
			}
			return
		}
	}
	t.Errorf("规则 %d 不存在", r.ID)
}

// TestRulesUpdateEnableEnabled 勾选 enabled 提交 → 保持启用
func TestRulesUpdateEnableEnabled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "改后", Type: models.RuleTypeBlacklist, Value: "v", Enabled: false, Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"name":     {"改后"},
		"type":     {r.Type},
		"value":    {"v2"},
		"priority": {"3"},
		"enabled":  {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d", r.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	rules, _ := s.ListRules(false)
	for _, g := range rules {
		if g.ID == r.ID {
			if !g.Enabled || g.Value != "v2" {
				t.Errorf("更新未生效: %+v, want enabled=true value=v2", g)
			}
			return
		}
	}
	t.Errorf("规则 %d 不存在", r.ID)
}

// TestRulesDelete POST /admin/rules/{id}/delete → 302 + 规则消失（另有启用规则）
func TestRulesDelete(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "待删", Type: models.RuleTypeBlacklist, Value: "x", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(models.Rule{Name: "保底", Type: models.RuleTypeBlacklist, Value: "中介", Enabled: true, Priority: 2}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d/delete", r.ID), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/rules/%d/delete status = %d, want 302", r.ID, rec.Code)
	}
	rules, err := s.ListRules(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rules {
		if g.ID == r.ID {
			t.Errorf("删除未生效: %+v", g)
		}
	}
}

// TestRulesCreateInvalid 非法参数 → 400：坏 type / 空 value / 坏 priority（mode 废弃不校验）
func TestRulesCreateInvalid(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	cases := []struct {
		name string
		form url.Values
	}{
		{"坏 type", url.Values{"name": {"n"}, "type": {"bad_type"}, "value": {"v"}, "priority": {"1"}}},
		{"空 value", url.Values{"name": {"n"}, "type": {models.RuleTypeBlacklist}, "value": {""}, "priority": {"1"}}},
		{"坏 priority", url.Values{"name": {"n"}, "type": {models.RuleTypeBlacklist}, "value": {"v"}, "priority": {"abc"}}},
		{"旧 hard type", url.Values{"name": {"n"}, "type": {"hard_keyword"}, "value": {"v"}, "priority": {"1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/rules", strings.NewReader(tc.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
	// 非法请求不应落库
	if rules, _ := s.ListRules(false); len(rules) != 0 {
		t.Errorf("非法参数后 DB 规则 = %+v, want 0 条", rules)
	}
}

// TestRulesUpdateInvalid 更新非法参数（坏 type / id<=0）→ 400
func TestRulesUpdateInvalid(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "守", Type: models.RuleTypeBlacklist, Value: "v", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 坏 type
	form := url.Values{"name": {"守"}, "type": {"bad"}, "value": {"v2"}, "priority": {"1"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d", r.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("坏 type status = %d, want 400", rec.Code)
	}
	// 缺 name（hidden 篡改面：与 createRule 校验对称）
	form2 := url.Values{"type": {models.RuleTypeBlacklist}, "value": {"v2"}, "priority": {"1"}}
	req1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d", r.ID), strings.NewReader(form2.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest {
		t.Errorf("缺 name status = %d, want 400", rec1.Code)
	}
	// id <= 0
	req2 := httptest.NewRequest(http.MethodPost, "/admin/rules/0", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("id=0 status = %d, want 400", rec2.Code)
	}
	// 非法请求不应改库
	rules, _ := s.ListRules(false)
	if rules[0].Value != "v" {
		t.Errorf("非法更新改库了: %+v", rules)
	}
}

// TestRulesMethodNotAllowed GET 写操作路由必须 405 且不落库（钉死「GET 链接触发写库」漏洞）
func TestRulesMethodNotAllowed(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "GET 靶", Type: models.RuleTypeBlacklist, Value: "v", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 <a>/<img> 链接触发写操作：GET + query 携带全部参数
	for _, path := range []string{
		fmt.Sprintf("/admin/rules/%d?name=GET靶&type=%s&value=偷改&priority=1&enabled=on", r.ID, r.Type),
		fmt.Sprintf("/admin/rules/%d/delete", r.ID),
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s status = %d, want 405", path, rec.Code)
		}
	}
	rules, _ := s.ListRules(false)
	if len(rules) != 1 || rules[0].Value != "v" {
		t.Errorf("GET 触发了写库: %+v, want 原值 v", rules)
	}
}

// TestRulesTokenPropagation 鉴权开启 + ?token=secret：
// GET /admin/rules 302 到配置 tab；配置页表单 action 透传 token；写操作 PRG 带回 token
func TestRulesTokenPropagation(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	r, err := s.CreateRule(models.Rule{Name: "鉴权规则", Type: models.RuleTypeBlacklist, Value: "x", Enabled: true, Priority: 5})
	if err != nil {
		t.Fatal(err)
	}

	// 无 token → 401（对照：鉴权确实生效）
	req0 := httptest.NewRequest(http.MethodGet, "/admin/rules", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusUnauthorized {
		t.Errorf("GET /admin/rules 无 token status = %d, want 401", rec0.Code)
	}

	// GET /admin/rules?token=secret → 302，Location 带 token
	req := httptest.NewRequest(http.MethodGet, "/admin/rules?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /admin/rules?token=secret status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/config?tab=rules&token=secret" {
		t.Fatalf("Location = %q, want /admin/config?tab=rules&token=secret", loc)
	}

	// 配置规则 Tab：表单/链接透传 token
	req1 := httptest.NewRequest(http.MethodGet, "/admin/config?tab=rules&token=secret", nil)
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("GET /admin/config?tab=rules&token=secret status = %d, want 200", rec1.Code)
	}
	body := rec1.Body.String()
	for _, want := range []string{
		"/admin/rules?token=secret",                              // 新增表单 action
		fmt.Sprintf("/admin/rules/%d?token=secret", r.ID),        // 行内保存表单 action
		fmt.Sprintf("/admin/rules/%d/delete?token=secret", r.ID), // 行内删除表单 action
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q（token 未透传）", want)
		}
	}

	// POST 更新带 token → 302 且 Location 带回 token
	form := url.Values{"name": {"鉴权规则"}, "type": {r.Type}, "value": {"y"}, "priority": {"1"}, "enabled": {"on"}}
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d?token=secret", r.ID), strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Errorf("POST 更新 status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/admin/config?tab=rules&token=secret" {
		t.Errorf("Location = %q, want /admin/config?tab=rules&token=secret", loc)
	}
	rules, _ := s.ListRules(false)
	if rules[0].Value != "y" {
		t.Errorf("鉴权下更新未生效: %+v", rules)
	}
}

// TestRulesEnsureDefaultOnEmptyTab 启用规则为 0 时打开 rules tab → 种子默认地点
func TestRulesEnsureDefaultOnEmptyTab(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/config?tab=rules", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "默认地点") {
		t.Errorf("页面缺默认地点")
	}
	rules, err := s.ListRules(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("启用规则 = %d, want 1", len(rules))
	}
	r := rules[0]
	if r.Name != "默认地点" || r.Type != models.RuleTypeWhitelist ||
		r.Value != "雍和宫,和平里" || !r.Enabled || r.Priority != 100 {
		t.Errorf("默认规则不符: %+v", r)
	}
}

// TestRulesDeleteLastEnabled 删除唯一启用规则 → 400，规则仍在
func TestRulesDeleteLastEnabled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "唯一", Type: models.RuleTypeBlacklist, Value: "x", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d/delete", r.ID), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "至少保留一条启用规则") {
		t.Errorf("body = %q", rec.Body.String())
	}
	rules, _ := s.ListRules(false)
	if len(rules) != 1 || rules[0].ID != r.ID {
		t.Errorf("不应删掉: %+v", rules)
	}
}

// TestRulesDisableLastEnabled 禁用唯一启用规则 → 400，仍启用
func TestRulesDisableLastEnabled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	r, err := s.CreateRule(models.Rule{Name: "唯一", Type: models.RuleTypeBlacklist, Value: "v", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"name":     {"唯一"},
		"type":     {r.Type},
		"value":    {"v2"},
		"priority": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/rules/%d", r.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rules, _ := s.ListRules(false)
	if len(rules) != 1 || !rules[0].Enabled || rules[0].Value != "v" {
		t.Errorf("不应改库: %+v", rules)
	}
}
