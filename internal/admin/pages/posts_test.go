package pages_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"rent-scout/internal/admin/pages"
	"rent-scout/internal/admin/testutil"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/security/actionref"
	"rent-scout/internal/store"
	"strings"
	"testing"
	"time"
)

type stubNotifyManual struct {
	ids   []int64
	group string
	err   error
}

func (s *stubNotifyManual) SendSelected(ctx context.Context, ids []int64, group string) error {
	s.ids = append([]int64(nil), ids...)
	s.group = group
	return s.err
}

func TestHandleNotifySelected(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	stub := &stubNotifyManual{}
	srv.SetNotifyManual(stub)

	form := url.Values{}
	form.Add("post_id", "11")
	form.Add("post_id", "22")
	form.Add("post_id", "11")
	req := httptest.NewRequest(http.MethodPost, "/admin/notify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	msg := loc.Query().Get("msg")
	if !strings.Contains(msg, "已发送 2 条") || !strings.Contains(msg, "手动触发-") {
		t.Errorf("msg=%q", msg)
	}
	if len(stub.ids) != 2 || stub.ids[0] != 11 || stub.ids[1] != 22 {
		t.Errorf("ids=%v, want [11 22]", stub.ids)
	}
	if !strings.HasPrefix(stub.group, "手动触发-") {
		t.Errorf("group=%q", stub.group)
	}
}

func TestHandleNotifySelectedEmpty(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	stub := &stubNotifyManual{}
	srv.SetNotifyManual(stub)

	req := httptest.NewRequest(http.MethodPost, "/admin/notify", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d, want 303", rec.Code)
	}
	if stub.ids != nil {
		t.Errorf("空勾选不应调用 SendSelected, ids=%v", stub.ids)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("msg") != "请先勾选帖子" {
		t.Errorf("msg=%q", loc.Query().Get("msg"))
	}
}

func TestAdminPostsOnboardWhenSourcesOff(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "还没开始采集") {
		t.Error("源全关时应提示还没开始采集")
	}
	if !strings.Contains(body, "去配置采集") {
		t.Error("应有去配置采集链接")
	}
	if !strings.Contains(body, "还没有帖子。先启用采集源并贴上 Cookie") {
		t.Error("空列表应提示先启用采集源并贴 Cookie")
	}
	if !strings.Contains(body, `id="posts-collector-onboard"`) {
		t.Error("应有帖子页采集引导横幅")
	}
	if !strings.Contains(body, `data-dismiss-key="collector-not-started"`) {
		t.Error("应有 dismiss key")
	}
	if !strings.Contains(body, "不再显示") {
		t.Error("应有不再显示按钮")
	}
	if !strings.Contains(body, "粘贴原文") {
		t.Error("应指引 Cookie 粘贴方式")
	}
}

func TestAdminPostsHasBatchSelect(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "batch-1", Title: "批量勾选",
		CollectedAt: time.Now(), Status: models.PostStatusPassed,
	}); err != nil {
		t.Fatal(err)
	}
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="batch-notify-form"`) {
		t.Error("缺批量通知表单")
	}
	if !strings.Contains(body, `id="select-all-posts"`) {
		t.Error("缺本页全选")
	}
	if !strings.Contains(body, `form="batch-notify-form"`) {
		t.Error("勾选框应挂到批量表单，避免和已查看表单嵌套")
	}
	if !strings.Contains(body, "批量通知") {
		t.Error("批量通知条应常显，不要勾选后才出现")
	}
	if strings.Contains(body, `id="batch-bar" class="hidden`) {
		t.Error("批量条不应默认 hidden")
	}
	if strings.Contains(body, `id="tag-drawer"`) || strings.Contains(body, "选择标签") {
		t.Error("标签筛选不应再做成抽屉")
	}
	if !strings.Contains(body, "发送通知") {
		t.Error("缺发送通知按钮")
	}
}

func TestToggleFilterTags(t *testing.T) {
	if got := pages.ToggleFilterTags("", "望京"); got != "望京" {
		t.Errorf("空选加上望京 = %q", got)
	}
	if got := pages.ToggleFilterTags("望京", "中介"); got != "望京,中介" {
		t.Errorf("再选中介 = %q", got)
	}
	if got := pages.ToggleFilterTags("望京,中介", "望京"); got != "中介" {
		t.Errorf("取消望京 = %q", got)
	}
	if got := pages.ToggleFilterTags("中介", "中介"); got != "" {
		t.Errorf("取消最后一个应清空 = %q", got)
	}
}

func TestSplitFilterTagPreview(t *testing.T) {
	var tags []models.FilterTag
	for i := 0; i < 12; i++ {
		tags = append(tags, models.FilterTag{Text: string(rune('A' + i)), Count: 12 - i})
	}
	top, more := pages.SplitFilterTagPreview(tags, 10)
	if len(top) != 10 || len(more) != 2 {
		t.Fatalf("top=%d more=%d, want 10+2", len(top), len(more))
	}
	if top[0].Text != "A" || more[0].Text != "K" {
		t.Errorf("顺序不对 top0=%s more0=%s", top[0].Text, more[0].Text)
	}
	if !pages.FilterTagsContain(more, "K") || pages.FilterTagsContain(top, "K") {
		t.Error("K 应在 more")
	}
}

// TestAdminPage 帖子全览页：GET /admin 200 含全部标题；?status=passed 过滤只显示 passed 帖
func TestAdminPage(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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
	} else if !strings.Contains(body, "开始使用") || !strings.Contains(body, "去配置采集") {
		t.Errorf("介绍页应有首次使用清单")
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

	if _, err := s.InsertPost(models.RentPost{Source: "weibo", ExternalID: "wb-page", Title: "微博全览帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	if code, body := get("/admin/posts"); code != http.StatusOK {
		t.Errorf("GET /admin/posts 含微博 status=%d", code)
	} else if !strings.Contains(body, "信息源") || !strings.Contains(body, "豆瓣") || !strings.Contains(body, "微博") {
		t.Errorf("全览应有信息源筛选")
	}
	if code, body := get("/admin/posts?source=weibo"); code != http.StatusOK {
		t.Errorf("source=weibo status=%d", code)
	} else {
		if !strings.Contains(body, "微博全览帖") {
			t.Errorf("weibo 过滤缺微博帖")
		}
		if strings.Contains(body, "标题0") {
			t.Errorf("weibo 过滤混入豆瓣帖")
		}
	}
}

// TestAdminPageFilters q/tag/handled 筛选 + 标签 chips + 已处理按钮
func TestAdminPageFilters(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "chip1", Title: "望京合租帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	chip1ID := testutil.PostID(t, s, "chip1")
	if err := s.ReplaceSystemTags(chip1ID, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
		{Kind: models.TagKindLocation, Text: "14号线", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "chip2", Title: "其它帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "blk1", Title: "中介房源", Status: models.PostStatusRejected}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: testutil.PostID(t, s, "blk1"), Status: models.PostStatusRejected, Stage: models.StageHardRule,
		RejectedBy: "黑名单命中:中介", DecidedAt: time.Now(),
		HardRules: []models.RuleHit{{Reason: "中介"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(testutil.PostID(t, s, "blk1"), []models.PostTag{
		{Kind: models.TagKindBlock, Text: "中介", Source: models.TagSourceSystem},
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
		t.Errorf("页面缺 location 标签 chips")
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
		t.Errorf("无标签帖应显示空态「无标签」: code=%d", code)
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

	if code, body := get("/admin/posts?tag=望京,中介"); code != http.StatusOK {
		t.Fatalf("tag 多选 status = %d", code)
	} else {
		if !strings.Contains(body, "望京合租帖") || !strings.Contains(body, "中介房源") || strings.Contains(body, "其它帖") {
			t.Errorf("tag=望京,中介 应按或命中两条: %s", body)
		}
		if !strings.Contains(body, ">望京</a>") || !strings.Contains(body, ">中介</a>") {
			t.Error("多选应同时高亮两个标签")
		}
	}

	if code, body := get("/admin/posts?tag=望京"); code != http.StatusOK {
		t.Fatalf("全部清空对照 status = %d", code)
	} else if !strings.Contains(body, `href="/admin/posts"`) {
		t.Error("点全部应清掉 tag 多选")
	}
}

func TestAdminTagFilterPreview(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)
	for i := 0; i < 12; i++ {
		ext := fmt.Sprintf("tagp%d", i)
		text := fmt.Sprintf("tag%02d", i)
		if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: ext, Title: "预览" + text, Status: models.PostStatusPassed}); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceSystemTags(testutil.PostID(t, s, ext), []models.PostTag{
			{Kind: models.TagKindLocation, Text: text, Source: models.TagSourceSystem},
		}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
		return rec.Body.String()
	}
	body := get("/admin/posts")
	if !strings.Contains(body, "更多") || !strings.Contains(body, `class="tag-more"`) {
		t.Error("超过 10 个标签应出现更多")
	}
	if strings.Contains(body, `id="tag-drawer"`) {
		t.Error("不应再有抽屉")
	}
	body = get("/admin/posts?tag=tag10")
	if !strings.Contains(body, `class="tag-more" open`) {
		t.Error("选中折叠区标签应自动展开")
	}
}

func TestAdminPostsPagination(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "mark1", Title: "标记帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "mark1")

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
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Kind != models.TagKindFeedback || tags[0].Text != "有用" {
		t.Errorf("DB 标签 = %+v, want 仅 feedback 有用（备注不进标签）", tags)
	}

	// 非法 action → 400
	if rec := post("bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("非法 action status = %d, want 400", rec.Code)
	}
}

// TestAdminMarkMethodNotAllowed GET /admin/mark 必须 405 且不写库：
// 钉死「GET 链接触发写库」漏洞（mux 不限方法 + FormValue 并入 query）。
func TestAdminMarkMethodNotAllowed(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "getmark", Title: "GET 标记", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "getmark")

	// 模拟 <a>/<img> 链接触发：GET + query 携带全部写库参数
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/mark?post_id=%d&action=useful&reason=x", id), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /admin/mark status = %d, want 405", rec.Code)
	}
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("GET 触发了写库：DB 标签 = %+v, want 0 条", tags)
	}
}

// TestAdminMarkInvalidPostID post_id=0 → 400（审查点名未测分支）
func TestAdminMarkInvalidPostID(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
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
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "tok1", Title: "鉴权帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "tok1")

	// 无 token → 302 重定向到登录页
	req0 := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusFound || !strings.Contains(rec0.Header().Get("Location"), "/admin/login") {
		t.Errorf("GET /admin 无 token status = %d, want 302", rec0.Code)
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
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Kind != models.TagKindFeedback || tags[0].Text != "有用" {
		t.Errorf("DB 标签 = %+v, want 仅 feedback 有用（备注不进标签）", tags)
	}
}

// TestAdminHandled 独立已处理写/清：POST handled=1/0 → 302 + HandledAt；非法参数 400；不写反馈
func TestAdminHandled(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h1", Title: "处理帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "h1")

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
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("已处理不应写标签: %+v", tags)
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

// TestStatsPage 统计页：GET /admin/stats 200 含今日计数、渠道成功率、死信行
func TestStatsPage(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	now := time.Now()
	// 今日 2 帖采集 + 今日判定 1 passed / 1 rejected
	for i := 0; i < 2; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("st%d", i), Title: "t",
			CollectedAt: now, Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: testutil.PostID(t, s, "st0"), Status: models.PostStatusPassed,
		Stage: models.StageHardRule, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: testutil.PostID(t, s, "st1"), Status: models.PostStatusRejected,
		Stage: models.StageHardRule, RejectedBy: "x", DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	// 昨日帖 + 昨日判定：不得计入今日计数（边界过滤）
	yesterday := now.AddDate(0, 0, -1)
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "st-yesterday", Title: "t",
		CollectedAt: yesterday, Status: models.PostStatusCollected}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: testutil.PostID(t, s, "st-yesterday"), Status: models.PostStatusPassed,
		Stage: models.StageHardRule, DecidedAt: yesterday}); err != nil {
		t.Fatal(err)
	}

	// 渠道：feishu sent×1 + dead×1 → 成功率 50%
	sentID := testutil.PostID(t, s, "st0")
	if _, err := s.InsertNotification(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	deadID := testutil.PostID(t, s, "st1")
	if _, err := s.InsertNotification(deadID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(deadID, "feishu", "403 无权限"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/stats status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"统计报表", "今日采集",
		"feishu", "50%",
		"403 无权限",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q", want)
		}
	}
}

// TestDeadReset 死信重发：POST /admin/dead/reset → 302 + dead→pending（FetchPendingNotifications 可见）；
// 再点 → 302 + 提示"该通知非死信"渲染在统计页
func TestDeadReset(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "dead1", Title: "死信帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "dead1")
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(id, "feishu", "403"); err != nil {
		t.Fatal(err)
	}

	post := func() *httptest.ResponseRecorder {
		form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "channel": {"feishu"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 首次：302 回统计页 + dead→pending
	rec := post()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST reset status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/stats" {
		t.Errorf("Location = %q, want /admin/stats", loc)
	}
	pending, err := s.FetchPendingNotifications("feishu", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PostID != id || pending[0].Status != models.NotifyStatusPending {
		t.Errorf("重发后 pending = %+v, want 1 条 post_id=%d status=pending", pending, id)
	}

	// 再点：非 dead → 302 + 提示
	rec2 := post()
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("二次 reset status = %d, want 302", rec2.Code)
	}
	loc := rec2.Header().Get("Location")
	if !strings.Contains(loc, "msg=") {
		t.Fatalf("Location = %q, want 含 msg 提示", loc)
	}
	req := httptest.NewRequest(http.MethodGet, loc, nil)
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req)
	if !strings.Contains(rec3.Body.String(), "该通知非死信") {
		t.Errorf("统计页缺提示「该通知非死信」")
	}
}

// TestDeadResetInvalid 非法参数 → 400：post_id=0 / 空 channel；GET → 405（防 GET 链接触发写库）
func TestDeadResetInvalid(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "", nil)

	// GET + query 携带写库参数 → 405
	req0 := httptest.NewRequest(http.MethodGet, "/admin/dead/reset?post_id=1&channel=feishu", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET reset status = %d, want 405", rec0.Code)
	}

	// post_id=0 → 400
	form := url.Values{"post_id": {"0"}, "channel": {"feishu"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("post_id=0 status = %d, want 400", rec.Code)
	}

	// 空 channel → 400
	form2 := url.Values{"post_id": {"1"}, "channel": {""}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/dead/reset", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("空 channel status = %d, want 400", rec2.Code)
	}
}

// TestStatsTokenPropagation 鉴权开启 + ?token=secret：统计页表单 action 透传 token；
// POST 重发带 token → 302 且 Location 带回 token（PRG 后不 401）
func TestStatsTokenPropagation(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "tokdead", Title: "t", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "tokdead")
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(id, "feishu", "403"); err != nil {
		t.Fatal(err)
	}

	// 无 token → 302 重定向到登录页
	req0 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rec0 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec0, req0)
	if rec0.Code != http.StatusFound || !strings.Contains(rec0.Header().Get("Location"), "/admin/login") {
		t.Errorf("GET /admin/stats 无 token status = %d, want 302", rec0.Code)
	}

	// 有 token → 200 且表单 action 透传 token
	req := httptest.NewRequest(http.MethodGet, "/admin/stats?token=secret", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/stats?token=secret status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"/admin/stats?token=secret",      // nav 统计链接
		"/admin/logs?token=secret",       // nav 日志链接
		"/admin/dead/reset?token=secret", // 重发表单 action
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺 %q（token 未透传）", want)
		}
	}

	// POST 重发带 token → 302 且 Location 带回 token
	form := url.Values{"post_id": {fmt.Sprintf("%d", id)}, "channel": {"feishu"}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/dead/reset?token=secret", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Errorf("POST reset status = %d, want 302", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/admin/stats?token=secret" {
		t.Errorf("Location = %q, want /admin/stats?token=secret", loc)
	}
}

func pref(id int64, secret string) string {
	return actionref.Seal(id, secret)
}

func fRef(id int64, action, secret, extra string) string {
	u := "/f?p=" + pref(id, secret) + "&action=" + action
	if extra != "" {
		u += extra
	}
	return u
}

func hRef(id int64, secret, extra string) string {
	u := "/h?p=" + pref(id, secret)
	if extra != "" {
		u += extra
	}
	return u
}

// feedbackSig 生成反馈链接签名（与 notifier.BuildFeedbackURL 同算法，供测试构造合法链接）
func feedbackSig(postID int64, action string, exp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d|%s|%d", postID, action, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestFeedbackNoToken：token 空（鉴权关闭）→ 不签名直接放行，任意合法参数 200
func TestFeedbackNoToken(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "", nil)

	req := httptest.NewRequest(http.MethodGet, fRef(1, "useful", "", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200（不签名模式）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("body 缺成功文案: %s", rec.Body.String())
	}
}

// TestFeedbackSigned：token 非空 → 无 sig/过期/错 sig 失败页；正确签名 200 + 写库；重复点击两次 200
func TestFeedbackSigned(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	// 无 sig → 失败页
	req := httptest.NewRequest(http.MethodGet, fRef(1, "useful", "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("无 sig: status = %d, want 200（失败页）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("无 sig: body 缺失败文案: %s", rec.Body.String())
	}

	// 过期 exp（过去时间戳）→ 失败页
	expired := time.Now().Add(-time.Hour).Unix()
	req = httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=%s", expired, feedbackSig(1, "useful", expired, "secret"))), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("过期: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 错误 sig（exp 未来有效，排除过期干扰）→ 失败页
	future := time.Now().Add(time.Hour).Unix()
	req = httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=deadbeef", future)), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("错 sig: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 正确签名 → 200 + 成功文案 + DB 有记录
	sig := feedbackSig(1, "useful", future, "secret")
	url := fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=%s", future, sig))
	req = httptest.NewRequest(http.MethodGet, url, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("正确签名: status=%d body=%s", rec.Code, rec.Body.String())
	}
	tags, err := s.ListTagsByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Kind != models.TagKindFeedback || tags[0].Text != "有用" {
		t.Errorf("DB 标签 = %+v, want 1 条 useful post=1", tags)
	}

	// 重复点击同一签名 → 两次都 200（v1 接受重复记录，报表按帖去重——RuleHitStats 已 COUNT(DISTINCT post_id)）
	for i := 0; i < 2; i++ {
		req = httptest.NewRequest(http.MethodGet, url, nil)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("重复点击 #%d: status = %d, want 200", i+1, rec.Code)
		}
	}
}

// TestFeedbackBadAction：非法 action → 400
func TestFeedbackBadAction(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, fRef(1, "bad", "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestFeedbackAuthDisabled：AuthRequired=false → 无效签名也放行（开关为准）
func TestFeedbackAuthDisabled(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()

	// 鉴权关闭 + token 非空（模拟有 token 但开关关闭的场景）
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: false}}, "secret", nil)

	// 无效签名 → 应放行（开关为准）
	future := time.Now().Add(time.Hour).Unix()
	req := httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=invalid", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=false 无效签名: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("AuthRequired=false 无效签名: body 缺成功文案: %s", rec.Body.String())
	}

	// 完全无 sig → 也应放行
	req = httptest.NewRequest(http.MethodGet, fRef(2, "useless", "secret", ""), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=false 无sig: status = %d, want 200", rec.Code)
	}
}

// TestFeedbackAuthEnabledInvalidSig：AuthRequired=true + token 非空 → 无效签名被拒绝
func TestFeedbackAuthEnabledInvalidSig(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()

	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	// 无效签名 → 应拒绝
	future := time.Now().Add(time.Hour).Unix()
	req := httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=invalid", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=true 无效签名: status = %d, want 200（失败页）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("AuthRequired=true 无效签名: body 缺失败文案: %s", rec.Body.String())
	}
}

// TestHandledLinkSigned：验签成功写 handled_at；错签失败；不写 feedbacks（Spec 09 §3.5）
func TestHandledLinkSigned(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h1", Title: "已处理帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "h1")
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	future := time.Now().Add(time.Hour).Unix()

	// 错签 → 失败页，HandledAt 仍空，无反馈
	req := httptest.NewRequest(http.MethodGet,
		hRef(id, "secret", fmt.Sprintf("&exp=%d&sig=deadbeef", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("错签: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt != nil {
		t.Fatalf("错签后不应写 HandledAt: ok=%v err=%v HandledAt=%v", ok, err, p.HandledAt)
	}
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("错签不应写标签: %d", len(tags))
	}

	// 正确签名 → 成功 + HandledAt + 仍无 feedbacks
	sig := feedbackSig(id, "handled", future, "secret")
	req = httptest.NewRequest(http.MethodGet,
		hRef(id, "secret", fmt.Sprintf("&exp=%d&sig=%s", future, sig)), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已标记为已处理") {
		t.Errorf("正签: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err = s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("正签后应写 HandledAt: ok=%v err=%v", ok, err)
	}
	tags, err = s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("已处理入口不应写标签: %+v", tags)
	}
}

// TestHandledLinkAuthDisabled：鉴权关 → 无签也可标记，仍不写 feedbacks
func TestHandledLinkAuthDisabled(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h2", Title: "开放已处理", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "h2")
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: false}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, hRef(id, "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已标记为已处理") {
		t.Errorf("鉴权关: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("鉴权关应写 HandledAt: ok=%v err=%v", ok, err)
	}
	tags, err := s.ListTagsByPost(id)
	if err != nil || len(tags) != 0 {
		t.Errorf("不应写标签: err=%v n=%d", err, len(tags))
	}
}

// TestAPIFeedbacksAuth 反馈写入鉴权矩阵（规格 7.1）：
// 管理 token 鉴权下 → 无 sig+Bearer 201；无 token 401；错 sig 401；正确 sig 201；非法 action 400
func TestAPIFeedbacksAuth(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	future := time.Now().Add(time.Hour).Unix()
	goodSig := feedbackSig(1, models.FeedbackUseful, future, "secret")

	cases := []struct {
		name   string
		bearer string // 空 = 不带 Authorization
		sig    string // 空 = 不带签名参数
		action string
		want   int
	}{
		{"无 sig + Bearer", "secret", "", models.FeedbackUseful, http.StatusCreated},
		{"无 token", "", "", models.FeedbackUseful, http.StatusUnauthorized},
		{"错 sig", "secret", "deadbeef", models.FeedbackUseful, http.StatusUnauthorized},
		{"正确 sig", "secret", goodSig, models.FeedbackUseful, http.StatusCreated},
		{"非法 action", "secret", "", "bad", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"post_id":1,"channel":"test","action":%q,"reason":"试试"}`, tc.action)
			req := httptest.NewRequest(http.MethodPost, "/api/feedbacks", strings.NewReader(body))
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.sig != "" {
				q := req.URL.Query()
				q.Set("exp", fmt.Sprintf("%d", future))
				q.Set("sig", tc.sig)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// 两次 201 均真实写库（无 sig 与正确 sig 各一条）
	tags, err := s.ListTagsByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	feedbackN := 0
	for _, tg := range tags {
		if tg.Kind == models.TagKindFeedback && tg.Text == "有用" {
			feedbackN++
		}
	}
	if feedbackN != 2 {
		t.Errorf("DB 标签 = %+v, want 2 条 useful post=1", tags)
	}
}

// TestAPIFeedbacksAuthOff 鉴权关闭（AuthRequired=false）时以开关为准：
// 即使配置了 server token，无 sig / 带 sig 一律放行 201（不验证）；畸形 JSON 仍 400
func TestAPIFeedbacksAuthOff(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{}, "secret", nil)

	future := time.Now().Add(time.Hour).Unix()

	cases := []struct {
		name string
		body string
		sig  string
		want int
	}{
		{"无 sig 放行", `{"post_id":1,"channel":"test","action":"useful"}`, "", http.StatusCreated},
		{"带 sig 放行（不验证）", `{"post_id":1,"channel":"test","action":"useful"}`, "deadbeef", http.StatusCreated},
		{"畸形 JSON", `{bad`, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/feedbacks", strings.NewReader(tc.body))
			if tc.sig != "" {
				q := req.URL.Query()
				q.Set("exp", fmt.Sprintf("%d", future))
				q.Set("sig", tc.sig)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// 两次 201 均真实写库（无 sig 与带 sig 各一条）
	tags, err := s.ListTagsByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	feedbackN := 0
	for _, tg := range tags {
		if tg.Kind == models.TagKindFeedback && tg.Text == "有用" {
			feedbackN++
		}
	}
	if feedbackN != 2 {
		t.Errorf("DB 标签 = %+v, want 2 条 useful post=1", tags)
	}
}

// TestAPIPostsList 列表：status 过滤 / limit+offset 分页 / 空 status 全量
func TestAPIPostsList(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	// 播种 3 帖：passed、rejected、collected（id 倒序 = list2, list1, list0）
	for i, status := range []string{models.PostStatusPassed, models.PostStatusRejected, models.PostStatusCollected} {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("list%d", i), Title: fmt.Sprintf("帖%d", i), Status: status}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) (int, []models.RentPost) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var out struct {
			Posts []models.RentPost `json:"posts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s 解析响应失败: %v (body=%s)", path, err, rec.Body.String())
		}
		return rec.Code, out.Posts
	}

	if code, posts := get("/api/posts?status=passed"); code != http.StatusOK || len(posts) != 1 || posts[0].Status != models.PostStatusPassed {
		t.Errorf("status=passed: code=%d posts=%+v, want 1 条 passed", code, posts)
	}
	if code, posts := get("/api/posts?limit=1&offset=1"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "list1" {
		t.Errorf("limit=1&offset=1: code=%d posts=%+v, want 1 条 list1（id 倒序第二条）", code, posts)
	}
	if code, posts := get("/api/posts"); code != http.StatusOK || len(posts) != 3 {
		t.Errorf("空 status: code=%d len=%d, want 3 条全量", code, len(posts))
	}
}

// TestAPIPostsListFilters API 透传 q/tag/handled（规格 §6）
func TestAPIPostsListFilters(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	p1 := models.RentPost{Source: "douban", ExternalID: "af1", Title: "望京合租", Content: "近地铁",
		Status: models.PostStatusPassed}
	p2 := models.RentPost{Source: "douban", ExternalID: "af2", Title: "回龙观", Content: "无",
		Status: models.PostStatusPassed}
	for _, p := range []models.RentPost{p1, p2} {
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	id1 := testutil.PostID(t, s, "af1")
	id2 := testutil.PostID(t, s, "af2")
	if err := s.ReplaceSystemTags(id1, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(id2, []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPostHandled(id2); err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, []models.RentPost) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var out struct {
			Posts []models.RentPost `json:"posts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s 解析失败: %v", path, err)
		}
		return rec.Code, out.Posts
	}

	if code, posts := get("/api/posts?q=合租"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "af1" {
		t.Errorf("q=合租: code=%d posts=%+v", code, posts)
	}
	if code, posts := get("/api/posts?tag=望京"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "af1" {
		t.Errorf("tag=望京: code=%d posts=%+v", code, posts)
	}
	if code, posts := get("/api/posts?handled=1"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "af2" || posts[0].HandledAt == nil {
		t.Errorf("handled=1: code=%d posts=%+v", code, posts)
	}
	if code, posts := get("/api/posts?handled=0"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "af1" {
		t.Errorf("handled=0: code=%d posts=%+v", code, posts)
	}
	if _, err := s.InsertPost(models.RentPost{Source: "weibo", ExternalID: "wb-api", Title: "微博API", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	if code, posts := get("/api/posts?source=weibo"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "wb-api" {
		t.Errorf("source=weibo: code=%d posts=%+v", code, posts)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/post-tags", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-tags status = %d", rec.Code)
	}
	var tagsOut struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tagsOut); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tname := range tagsOut.Tags {
		got[tname] = true
	}
	if !got["望京"] || !got["回龙观"] {
		t.Errorf("post-tags = %v, want 含望京/回龙观", tagsOut.Tags)
	}
}

// TestAPIPostsListDefaultLimit：不传 limit → 默认 50（播种 55 帖验证默认值生效）
func TestAPIPostsListDefaultLimit(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	for i := 0; i < 55; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("d%d", i), Title: "t", Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Posts []models.RentPost `json:"posts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Posts) != 50 {
		t.Errorf("默认 limit 下返回 %d 条, want 50", len(out.Posts))
	}
}

// TestAPIPostDetail 详情：播种完整链路（post+filter_result+notification+feedback）→ 组合字段齐全；不存在 404
func TestAPIPostDetail(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "detail1", Title: "详情帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := testutil.PostID(t, s, "detail1")
	if err := s.SaveFilterResult(models.FilterResult{PostID: id, Status: models.PostStatusPassed, Stage: models.StageHardRule, DecidedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUserFeedback(id, models.FeedbackUseful, "不错"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/posts/%d", id), nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Post          models.RentPost       `json:"post"`
		FilterResult  models.FilterResult   `json:"filter_result"`
		Notifications []models.Notification `json:"notifications"`
		Tags          []models.PostTag      `json:"tags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析详情响应失败: %v (body=%s)", err, rec.Body.String())
	}
	if out.Post.ID != id || out.Post.Title != "详情帖" {
		t.Errorf("post 字段 = %+v, want id=%d title=详情帖", out.Post, id)
	}
	if out.FilterResult.Status != models.PostStatusPassed || out.FilterResult.Stage != models.StageHardRule {
		t.Errorf("filter_result = %+v, want passed/hard_rule", out.FilterResult)
	}
	if len(out.Notifications) != 1 || out.Notifications[0].Channel != "feishu" || out.Notifications[0].Status != models.NotifyStatusPending {
		t.Errorf("notifications = %+v, want 1 条 feishu", out.Notifications)
	}
	if len(out.Tags) != 1 || out.Tags[0].Kind != models.TagKindFeedback || out.Tags[0].Text != "有用" {
		t.Errorf("tags = %+v, want 仅 feedback 有用（备注不进标签）", out.Tags)
	}

	// 不存在 → 404
	req = httptest.NewRequest(http.MethodGet, "/api/posts/99999", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在: status = %d, want 404", rec.Code)
	}
}

// TestAPIPostsEdgeCases：路径/方法边界——空 id 400、非数字 id 400、POST 405
func TestAPIPostsEdgeCases(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	cases := []struct {
		name string
		path string
		meth string
		want int
	}{
		{"空 id", "/api/posts/", http.MethodGet, http.StatusBadRequest},
		{"非数字 id", "/api/posts/abc", http.MethodGet, http.StatusBadRequest},
		{"列表 POST", "/api/posts", http.MethodPost, http.StatusMethodNotAllowed},
		{"详情 POST", "/api/posts/1", http.MethodPost, http.StatusMethodNotAllowed},
		{"反馈 GET", "/api/feedbacks", http.MethodGet, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.meth, tc.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestNotifyTestPageHasButtons(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/config?tab=notifier", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Count(body, "检测连通性") < 2 {
		t.Errorf("飞书和 PushPlus 都应有检测连通性: %s", body)
	}
	if !strings.Contains(body, `data-channel="feishu"`) || !strings.Contains(body, `data-channel="pushplus"`) {
		t.Errorf("按钮应带渠道: missing data-channel")
	}
}

func TestNotifyTestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/config/notify/test", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestNotifyTestChannelFromQuery(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	form := url.Values{"secret.notifier.feishu.webhook": {"https://example.com/hook"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/notify/test?channel=feishu", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] == false && strings.Contains(fmtString(out["summary"]), "仅支持") {
		t.Fatalf("query channel 应识别飞书: %v", out)
	}
}

func TestNotifyTestRequiresChannel(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/config/notify/test", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Errorf("缺渠道应失败: %v", out)
	}
}

func TestNotifyTestFeishuNeedsWebhook(t *testing.T) {
	srv := newTestServer(t, config.DefaultApp(), "", nil)
	form := url.Values{"channel": {"feishu"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/notify/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != false || !strings.Contains(fmtString(out["summary"]), "Webhook") {
		t.Errorf("缺 webhook: %v", out)
	}
}

func TestNotifyTestMockWhenEmpty(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	probe := &testutil.StubNotifyProbe{}
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	srv.SetNotifyProbe(probe)
	form := url.Values{
		"channel":                        {"feishu"},
		"secret.notifier.feishu.webhook": {"https://example.com/hook"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/notify/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["mocked"] != true {
		t.Fatalf("空库应用 mock: %v", out)
	}
	if probe.Channel != "feishu" || len(probe.Items) != 2 {
		t.Fatalf("channel=%s items=%d", probe.Channel, len(probe.Items))
	}
	if !strings.Contains(probe.Items[0].Title, "连通检测") {
		t.Errorf("样例标题: %s", probe.Items[0].Title)
	}
}

func TestNotifyTestUsesRecentAnyStatus(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	p := models.RentPost{
		Source: "douban", ExternalID: "rej-1", Title: "被拒的帖子也能试发",
		URL: "https://example.com/p1", CollectedAt: time.Now(), Status: models.PostStatusRejected,
	}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	probe := &testutil.StubNotifyProbe{}
	srv := newTestServerWithStore(t, s, config.DefaultApp(), "", nil)
	srv.SetNotifyProbe(probe)
	form := url.Values{
		"channel":                        {"pushplus"},
		"secret.notifier.pushplus.token": {"tokentok"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/notify/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != true || out["mocked"] != false {
		t.Fatalf("应用库内帖: %v", out)
	}
	if probe.Channel != "pushplus" || len(probe.Items) != 1 || probe.Items[0].Title != "被拒的帖子也能试发" {
		t.Fatalf("items=%+v channel=%s", probe.Items, probe.Channel)
	}
	n, err := s.FetchPendingNotifications("pushplus", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(n) != 0 {
		t.Errorf("试发不应写通知账本: %d", len(n))
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

type fakeController struct {
	known   map[string]bool
	enabled map[string]bool
}

func (f *fakeController) Sources() []string {
	names := make([]string, 0, len(f.known))
	for n := range f.known {
		names = append(names, n)
	}
	return names
}

func (f *fakeController) SetEnabled(name string, on bool) error {
	if !f.known[name] {
		return fmt.Errorf("未知源 %s", name)
	}
	f.enabled[name] = on
	return nil
}

func (f *fakeController) SourceEnabled(name string) bool {
	return f.enabled[name]
}

func TestAPISourceActions(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	if err := s.SetProgress("douban", store.SourceProgress{Page: "1:0"}); err != nil {
		t.Fatal(err)
	}
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": true}}
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", ctrl)

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post("/api/sources/douban/enable"); code != http.StatusOK {
		t.Fatalf("enable = %d", code)
	}
	if code := post("/api/sources/douban/disable"); code != http.StatusOK {
		t.Fatalf("disable = %d", code)
	}
	if code := post("/api/sources/douban/reset"); code != http.StatusOK {
		t.Fatalf("reset = %d", code)
	}
	if _, ok, err := s.GetCursor("douban"); err != nil || ok {
		t.Fatalf("reset 后仍有游标 ok=%v err=%v", ok, err)
	}
	if code := post("/api/sources/unknown/enable"); code != http.StatusNotFound {
		t.Fatalf("unknown = %d, want 404", code)
	}
}

func TestAPISourcesUnavailable(t *testing.T) {
	s := testutil.NewAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
