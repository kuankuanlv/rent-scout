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

// TestAdminPage 帖子全览页：GET /admin 200 含全部标题；?status=passed 过滤只显示 passed 帖
func TestAdminPage(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	// 播种 3 帖：passed、rejected、collected
	for i, status := range []string{models.PostStatusPassed, models.PostStatusRejected, models.PostStatusCollected} {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("page%d", i), Title: fmt.Sprintf("标题%d", i), Status: status}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := get("/admin"); code != http.StatusOK {
		t.Errorf("GET /admin 介绍页 status = %d", code)
	} else if !strings.Contains(body, "租房侦察兵") || !strings.Contains(body, "微博") || !strings.Contains(body, "小红书") {
		t.Errorf("介绍页缺品牌与多源说明")
	} else if !strings.Contains(body, "https://github.com/kuankuanlv/rent-scout") {
		t.Errorf("介绍页仓库地址错误")
	} else if !strings.Contains(body, "SQLite") || !strings.Contains(body, "硬规则") {
		t.Errorf("介绍页缺流水线说明")
	} else if !strings.Contains(body, "🏠 首页") {
		t.Errorf("顶栏应有首页入口")
	}
	if code, body := get("/admin/posts"); code != http.StatusOK {
		t.Errorf("GET /admin/posts status = %d, want 200", code)
	} else {
		for _, title := range []string{"标题0", "标题1", "标题2"} {
			if !strings.Contains(body, title) {
				t.Errorf("页面缺标题 %q", title)
			}
		}
		if !strings.Contains(body, "爬取过滤规则") || !strings.Contains(body, "/admin/config?tab=rules") {
			t.Errorf("全览应有跳转规则页的按钮")
		}
		if !strings.Contains(body, ">标签<") {
			t.Errorf("列表应有标签列")
		}
	}

	// 过滤：只含 passed 帖
	if code, body := get("/admin/posts?status=passed"); code != http.StatusOK {
		t.Errorf("GET /admin?status=passed status = %d, want 200", code)
	} else {
		if !strings.Contains(body, "标题0") {
			t.Errorf("passed 过滤缺标题0")
		}
		if !strings.Contains(body, "通过") {
			t.Errorf("状态徽章应显示中文「通过」而不是英文码")
		}
		if strings.Contains(body, ">passed<") {
			t.Errorf("状态徽章不应直接打 passed")
		}
		if strings.Contains(body, "标题1") || strings.Contains(body, "标题2") {
			t.Errorf("passed 过滤混入非 passed 帖")
		}
	}
}

// TestAdminPageFilters q/tag/handled 筛选 + AddressTags chips + 已处理按钮
func TestAdminPageFilters(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	p := models.RentPost{Source: "douban", ExternalID: "chip1", Title: "望京合租帖", Status: models.PostStatusPassed,
		AddressTags: []string{"望京", "14号线"}}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "chip2", Title: "其它帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "blk1", Title: "中介房源", Status: models.PostStatusRejected}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: postID(t, s, "blk1"), Status: models.PostStatusRejected, Stage: models.StageHardRule,
		RejectedBy: "黑名单命中:中介", DecidedAt: time.Now(),
		HardRules: []models.RuleHit{{Reason: "中介"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(models.Rule{Name: "地点", Type: models.RuleTypeWhitelist, Value: "朝阳门", Enabled: true, Priority: 1}); err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, body := get("/admin/posts?q=合租")
	if code != http.StatusOK {
		t.Fatalf("q 筛选 status = %d", code)
	}
	if !strings.Contains(body, "望京合租帖") || strings.Contains(body, "其它帖") {
		t.Errorf("q=合租 结果异常: %s", body)
	}
	if !strings.Contains(body, "望京") || !strings.Contains(body, "14号线") {
		t.Errorf("页面缺 AddressTags chips")
	}

	if code, body := get("/admin/posts?status=rejected"); code != http.StatusOK {
		t.Fatalf("rejected 列表 status = %d", code)
	} else if !strings.Contains(body, "中介") || !strings.Contains(body, "bg-red-50") {
		t.Errorf("拒绝帖应展示黑名单命中词: %s", body)
	}
	if !strings.Contains(body, `name="handled"`) || !strings.Contains(body, "/admin/handled") {
		t.Errorf("页面缺已查看表单")
	}
	if !strings.Contains(body, "人工标记") || !strings.Contains(body, "mark-open-btn") {
		t.Errorf("页面缺人工标记按钮")
	}

	if code, body := get("/admin/posts"); code != http.StatusOK || !strings.Contains(body, "无标签") {
		t.Errorf("无 AddressTags 帖应显示空态「无标签」: code=%d", code)
	}

	if code, body := get("/admin/posts"); code != http.StatusOK {
		t.Fatalf("标签平铺 status = %d", code)
	} else {
		if !strings.Contains(body, `>标签</span>`) || strings.Contains(body, `id="post-tag-select"`) {
			t.Error("标签应是平铺枚举，不是下拉")
		}
		if !strings.Contains(body, "望京") {
			t.Error("标签平铺应含帖子里的地址标签")
		}
		if !strings.Contains(body, "共 ") || !strings.Contains(body, "页") {
			t.Error("全览应有分页条")
		}
	}

	if code, body := get("/admin/posts?tag=望京"); code != http.StatusOK || !strings.Contains(body, "望京合租帖") || strings.Contains(body, "其它帖") {
		t.Errorf("tag=望京: code=%d body 异常", code)
	} else if !strings.Contains(body, `bg-indigo-600`) || !strings.Contains(body, ">望京</a>") {
		t.Errorf("tag=望京 平铺未高亮选中")
	}
}

func TestAdminPostsPagination(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)
	for i := 0; i < 3; i++ {
		p := models.RentPost{
			Source: "douban", ExternalID: fmt.Sprintf("pg%d", i), Title: fmt.Sprintf("分页帖%d", i),
			Status: models.PostStatusPassed, PublishedAt: base.Add(time.Duration(i) * time.Hour),
		}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	code, body := get("/admin/posts?page_size=1")
	if code != http.StatusOK {
		t.Fatalf("page_size=1 status = %d", code)
	}
	if !strings.Contains(body, "分页帖2") || strings.Contains(body, "分页帖0") {
		t.Errorf("第1页应按发布时间倒序只含最新: %s", body)
	}
	if !strings.Contains(body, "共 3 条") || !strings.Contains(body, "第 1 / 3 页") {
		t.Errorf("分页文案异常: %s", body)
	}
	code, body = get("/admin/posts?page_size=1&page=3")
	if code != http.StatusOK {
		t.Fatalf("page=3 status = %d", code)
	}
	if !strings.Contains(body, "分页帖0") || strings.Contains(body, "分页帖2") {
		t.Errorf("第3页应是最早帖: %s", body)
	}
}

// TestAdminMark 标记反馈：POST /admin/mark 合法 → 302（PRG）+ DB 有记录；非法 action → 400
func TestAdminMark(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "mark1", Title: "标记帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "mark1")

	post := func(action string) *httptest.ResponseRecorder {
		form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "action": {action}, "reason": {"测试原因"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/mark", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 合法 → 302 重定向回 /admin（PRG 防重复提交）
	rec := post(models.FeedbackUseful)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("合法标记 status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/posts" {
		t.Errorf("Location = %q, want /admin/posts", loc)
	}
	// DB 有记录
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != models.FeedbackUseful || items[0].Reason != "测试原因" {
		t.Errorf("DB 反馈 = %+v, want 1 条 useful", items)
	}

	// 非法 action → 400
	if rec := post("bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("非法 action status = %d, want 400", rec.Code)
	}
}

// TestAdminMarkMethodNotAllowed GET /admin/mark 必须 405 且不写库：
// 钉死「GET 链接触发写库」漏洞（mux 不限方法 + FormValue 并入 query）。
func TestAdminMarkMethodNotAllowed(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "getmark", Title: "GET 标记", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "getmark")

	// 模拟 <a>/<img> 链接触发：GET + query 携带全部写库参数
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/mark?post_id=%d&action=useful&reason=x", id), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /admin/mark status = %d, want 405", rec.Code)
	}
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("GET 触发了写库：DB 反馈 = %+v, want 0 条", items)
	}
}

// TestAdminMarkInvalidPostID post_id=0 → 400（审查点名未测分支）
func TestAdminMarkInvalidPostID(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	form := url.Values{"post_id": {"0"}, "action": {models.FeedbackUseful}}
	req := httptest.NewRequest(http.MethodPost, "/admin/mark", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("post_id=0 status = %d, want 400", rec.Code)
	}
}

// TestAdminTokenPropagation 鉴权开启 + ?token=secret：
// 页面 nav/筛选链接与表单 action 均透传 token（不 401）；无 token 访问 401（对照鉴权生效）。
func TestAdminTokenPropagation(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "tok1", Title: "鉴权帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "tok1")

	// 无 token → 401（对照：鉴权确实生效）
	req0 := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusUnauthorized {
		t.Errorf("GET /admin 无 token status = %d, want 401", rec0.Code)
	}

	// GET /admin?token=secret → 200 且链接透传 token
	req := httptest.NewRequest(http.MethodGet, "/admin/posts?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/posts?token=secret status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"/admin/posts?token=secret",   // nav 帖子链接
		"/admin/stats?token=secret",   // nav 统计链接
		"/admin/config?token=secret",  // nav 配置链接
		"/admin/logs?token=secret",    // nav 日志链接
		"/admin/mark?token=secret",    // 表单 action（FilterQuery 用 template.URL）
		"/admin/handled?token=secret", // 已处理表单
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q（token 未透传）", want)
		}
	}
	// 状态筛选链接：html/template 会把 & 编成 &amp;
	if !strings.Contains(body, "/admin/posts?status=passed&amp;token=secret") &&
		!strings.Contains(body, "/admin/posts?status=passed&token=secret") {
		t.Errorf("页面缺状态筛选透传 token 的链接")
	}

	// POST /admin/mark?token=secret + 合法表单 → 302，且重定向带回 token（PRG 后不 401）
	form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "action": {models.FeedbackUseful}, "reason": {"鉴权下提交"}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/mark?token=secret", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Errorf("POST /admin/mark?token=secret status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/admin/posts?token=secret" {
		t.Errorf("Location = %q, want /admin?token=secret", loc)
	}
	items, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Reason != "鉴权下提交" {
		t.Errorf("DB 反馈 = %+v, want 1 条", items)
	}
}

// TestAdminHandled 独立已处理写/清：POST handled=1/0 → 302 + HandledAt；非法参数 400；不写反馈
func TestAdminHandled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h1", Title: "处理帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "h1")

	post := func(handled string) *httptest.ResponseRecorder {
		form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "handled": {handled}}
		req := httptest.NewRequest(http.MethodPost, "/admin/handled", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	rec := post("1")
	if rec.Code != http.StatusSeeOther {
		t.Errorf("标记已处理 status = %d, want 302", rec.Code)
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("HandledAt 未写入: ok=%v err=%v", ok, err)
	}
	if p.Status != models.PostStatusPassed {
		t.Errorf("status 被改成 %s", p.Status)
	}
	fb, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(fb) != 0 {
		t.Errorf("已处理不应写反馈: %+v", fb)
	}

	if rec := post("0"); rec.Code != http.StatusSeeOther {
		t.Errorf("清除已处理 status = %d, want 302", rec.Code)
	}
	p, ok, err = s.GetPost(id)
	if err != nil || !ok || p.HandledAt != nil {
		t.Fatalf("HandledAt 未清除: ok=%v HandledAt=%v err=%v", ok, p.HandledAt, err)
	}

	if rec := post("x"); rec.Code != http.StatusBadRequest {
		t.Errorf("非法 handled status = %d, want 400", rec.Code)
	}

	// 透传筛选 query
	form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "handled": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/handled?status=passed&q=处理", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("带筛选 status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "status=passed") || !strings.Contains(loc, "q=") {
		t.Errorf("Location = %q, want 透传 status/q", loc)
	}
}
