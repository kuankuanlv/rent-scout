package weibo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
)

const chaohuaPageJSON = `{
  "items":[{"mblog":{
    "id":"5123456789","mid":"5123456789","bid":"AbCdEf",
    "created_at":"Sun Aug 16 10:00:00 +0800 2026",
    "text":"望京整租两居 8000 微信 abc","isLongText":false,
    "user":{"id":123,"screen_name":"租房君"}
  }}],
  "moreInfo":{"params":{"since_id":"{\"max_id\":5123456788}","max_id":0}}
}`

type staticCookie string

func (s staticCookie) Get(context.Context, string) (string, error) { return string(s), nil }

func TestWeiboListParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax_proxy/chaohua/page" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Query().Get("flowId"), "_-_feed") {
			t.Errorf("flowId=%s", r.URL.Query().Get("flowId"))
		}
		if r.URL.Query().Get("since_id") != "" {
			t.Errorf("首页不应带 since_id")
		}
		if r.Header.Get("Cookie") == "" {
			t.Error("缺少 Cookie")
		}
		w.Write([]byte(chaohuaPageJSON))
	}))
	defer srv.Close()

	s := New(Options{
		SuperTopics: []string{"100808453110d9ea6a7b6fd15e79788cf55186"},
		AjaxBase:    srv.URL,
		Cookie:      staticCookie("SUB=x"),
		Client:      srv.Client(),
	})
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	items, next, err := s.ListInWindow(context.Background(), "", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d", len(items))
	}
	it := items[0]
	if it.ExternalID != "5123456789" || it.Author != "租房君" || it.Kind != "super" {
		t.Errorf("条目 %+v", it)
	}
	if !strings.Contains(it.Content, "望京整租两居") {
		t.Errorf("content=%q", it.Content)
	}
	if it.URL != "https://weibo.com/123/AbCdEf" {
		t.Errorf("url=%q", it.URL)
	}
	if next != "0:2" {
		t.Errorf("next=%q", next)
	}
}

func TestChaohuaSecondPageSendsSince(t *testing.T) {
	var sawSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since_id") == "" {
			w.Write([]byte(chaohuaPageJSON))
			return
		}
		sawSince = r.URL.Query().Get("since_id")
		if r.URL.Query().Get("page_common_ext") == "" {
			t.Error("第二页应带 page_common_ext")
		}
		w.Write([]byte(`{"items":[],"moreInfo":{"params":{}}}`))
	}))
	defer srv.Close()
	s := New(Options{
		SuperTopics: []string{"100808453110d9ea6a7b6fd15e79788cf55186"},
		AjaxBase:    srv.URL,
		Cookie:      staticCookie("SUB=x"),
		Client:      srv.Client(),
	})
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	_, next, err := s.ListInWindow(context.Background(), "", start, end)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ListInWindow(context.Background(), next, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sawSince, "max_id") {
		t.Errorf("since_id=%q", sawSince)
	}
}

func TestWeiboMultiURLRotation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("flowId"), "aaaaaaaaaa") {
			w.Write([]byte(`{"items":[]}`))
			return
		}
		w.Write([]byte(chaohuaPageJSON))
	}))
	defer srv.Close()
	s := New(Options{
		SuperTopics: []string{"aaaaaaaaaaaaaaaaaaaa", "100808453110d9ea6a7b6fd15e79788cf55186"},
		AjaxBase:    srv.URL,
		Cookie:      staticCookie("SUB=x"),
		Client:      srv.Client(),
	})
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	items, next, err := s.ListInWindow(context.Background(), "", start, end)
	if err != nil || len(items) != 0 || next != "1:0" {
		t.Fatalf("空页应变下一组: items=%d next=%q err=%v", len(items), next, err)
	}
	if s.SkipGroup("0:0") != "1:0" {
		t.Errorf("SkipGroup=%q", s.SkipGroup("0:0"))
	}
	if s.SkipGroup("1:0") != "" {
		t.Errorf("最后一组 SkipGroup 应空")
	}
}

func TestWeiboLoginWall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>请先登录后查看 passport.weibo.com</body></html>`))
	}))
	defer srv.Close()
	s := New(Options{
		SuperTopics: []string{"100808453110d9ea6a7b6fd15e79788cf55186"},
		AjaxBase:    srv.URL,
		Cookie:      staticCookie("SUB=x"),
		Client:      srv.Client(),
	})
	_, _, err := s.List(context.Background(), "")
	if !errors.Is(err, cookie.ErrCookieInvalid) {
		t.Fatalf("登录墙应判定 cookie 失效, err=%v", err)
	}
}

func TestOwnerCommentsOK0DoesNotFailDetail(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.Contains(r.URL.Path, "/comments/hotflow") {
			w.Write([]byte(`{"ok":0}`))
			return
		}
		t.Errorf("unexpected %s", r.URL.Path)
	}))
	defer srv.Close()
	s := New(Options{Client: srv.Client(), MobileBase: srv.URL, AjaxBase: srv.URL})
	post, err := s.Detail(context.Background(), collector.ListItem{
		ExternalID:  "5332468150307044",
		Kind:        "super",
		AuthorID:    "1",
		Content:     "只有图没有联系方式",
		PublishedAt: time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatal(err)
	}
	if post.Content != "只有图没有联系方式" {
		t.Errorf("content=%q", post.Content)
	}
	if hits != 1 {
		t.Errorf("应打一次评论接口, hits=%d", hits)
	}
}

func TestWeiboDetailUsesListContentNoHTTP(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`<html><body>不应请求详情</body></html>`))
	}))
	defer srv.Close()
	s := New(Options{Client: srv.Client()})
	post, err := s.Detail(context.Background(), collector.ListItem{
		ExternalID:  "1",
		URL:         srv.URL + "/detail/1",
		Title:       "标题",
		Author:      "a",
		PublishedAt: time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local),
		Content:     "列表正文",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Errorf("Detail 不应打详情页, hits=%d", hits)
	}
	if post.Content != "列表正文" || post.Source != "weibo" {
		t.Errorf("post=%+v", post)
	}
}

func TestWeiboDetailExpandTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "5123456791") {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Write([]byte(`<html><body><script>var $render_data = [{"status":{"text":"望京整租全文<img src=\"https://img.example.com/a.jpg\">微信 abc"}}] || [];</script></body></html>`))
	}))
	defer srv.Close()
	s := New(Options{Client: srv.Client(), DetailBase: srv.URL})
	post, err := s.Detail(context.Background(), collector.ListItem{
		ExternalID:  "5123456791",
		URL:         "https://weibo.com/1/x",
		Title:       "望京摘要",
		Content:     "望京摘要",
		NeedDetail:  true,
		PublishedAt: time.Date(2026, 8, 16, 10, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(post.Content, "望京整租全文") || !strings.Contains(post.Content, "微信 abc") {
		t.Errorf("content=%q", post.Content)
	}
	if strings.Contains(post.Content, "img.example.com") || strings.Contains(post.Raw, "img.example.com") {
		t.Errorf("不应保留图片 content=%q raw=%q", post.Content, post.Raw)
	}
}

func TestWeiboDescribeCursor(t *testing.T) {
	s := New(Options{
		SuperTopics: []string{"100808aaaaaaaaaaaaaaaaaa", "100808bbbbbbbbbbbbbbbbbb"},
	})
	got := s.DescribeCursor("")
	if !strings.Contains(got, "超话") || !strings.Contains(got, "第1页") {
		t.Errorf("空游标 = %s", got)
	}
	got = s.DescribeCursor("1:2")
	if !strings.Contains(got, "第2页") {
		t.Errorf("1:2 = %s", got)
	}
}

func TestParseProfileList(t *testing.T) {
	body := `{"ok":1,"data":{"list":[{
		"id":"5331788828246627","idstr":"5331788828246627","mid":"5331788828246627",
		"mblogid":"RdkYiyBk7","created_at":"Fri Aug 14 12:16:18 +0800 2026",
		"text_raw":"7号线百子湾 2300 电话15711317999","isLongText":false,
		"user":{"id":6342026928,"idstr":"6342026928","screen_name":"北京租房小编"}
	}]}}`
	items, err := parseProfileList(body, "6342026928")
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	it := items[0]
	if it.ExternalID != "5331788828246627" || it.Kind != "user" || it.Author != "北京租房小编" {
		t.Errorf("%+v", it)
	}
	if it.PublishedAt.Year() != 2026 || it.PublishedAt.Month() != 8 || it.PublishedAt.Day() != 14 {
		t.Errorf("time=%v", it.PublishedAt)
	}
}

func TestFilterWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	mk := func(id string, t time.Time) collector.ListItem {
		return collector.ListItem{ExternalID: id, PublishedAt: t}
	}
	start := time.Date(2026, 8, 6, 0, 0, 0, 0, loc)
	end := time.Date(2026, 8, 16, 18, 0, 0, 0, loc)
	in := []collector.ListItem{
		mk("a", time.Date(2026, 8, 16, 14, 51, 0, 0, loc)),
		mk("b", time.Date(2026, 7, 26, 22, 19, 0, 0, loc)),
		mk("c", time.Date(2026, 8, 16, 15, 43, 0, 0, loc)),
	}
	out := filterWindow(in, start, end)
	if len(out) != 2 {
		t.Fatalf("len=%d want 2", len(out))
	}
}

func TestParseChaohuaPage(t *testing.T) {
	items, since, err := parseChaohuaPage(chaohuaPageJSON)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if items[0].Kind != "super" || !strings.Contains(items[0].Content, "8000") {
		t.Errorf("%+v", items[0])
	}
	if !strings.Contains(since, "max_id") {
		t.Errorf("since=%q", since)
	}
}

func TestSuperSkipsWithoutCookie(t *testing.T) {
	s := New(Options{SuperTopics: []string{"100808453110d9ea6a7b6fd15e79788cf55186"}})
	items, next, err := s.ListInWindow(context.Background(), "", time.Now().Add(-10*24*time.Hour), time.Now())
	if err != nil || len(items) != 0 {
		t.Fatalf("应跳过超话 items=%d err=%v", len(items), err)
	}
	if next != "" {
		t.Errorf("只有超话时应结束 next=%q", next)
	}
}

func TestWeiboRiskHTTPAndJSON(t *testing.T) {
	if err := weiboResponseErr(432, ""); err == nil || !errors.Is(err, cookie.ErrCookieInvalid) {
		t.Fatalf("432: %v", err)
	}
	if err := weiboResponseErr(200, `{"ok":-100,"url":"https://passport.weibo.com/sso/signin"}`); err == nil || !errors.Is(err, cookie.ErrCookieInvalid) {
		t.Fatalf("ok-100: %v", err)
	}
	if err := weiboResponseErr(200, `{"ok":0,"msg":"这里还没有内容","data":{"cards":[]}}`); err != nil {
		t.Fatalf("没内容不应当错误: %v", err)
	}
	if err := weiboResponseErr(200, `{"ok":0,"error_code":20101}`); err != nil {
		t.Fatalf("评论空结果不应当错误: %v", err)
	}
	if err := weiboResponseErr(200, `{"ok":1,"data":{}}`); err != nil {
		t.Fatalf("ok1: %v", err)
	}
}

func TestWeiboWatermarkMeta(t *testing.T) {
	s := New(Options{
		SuperTopics: []string{"100808453110d9ea6a7b6fd15e79788cf55186"},
		Users:       []string{"6342026928"},
	})
	if !s.TimeOrdered("") || s.WatermarkKey("") != "super:100808453110d9ea6a7b6fd15e79788cf55186" {
		t.Fatalf("超话应有序 key=%s ordered=%v", s.WatermarkKey(""), s.TimeOrdered(""))
	}
	if !s.TimeOrdered("1:0") || s.WatermarkKey("1:0") != "user:6342026928" {
		t.Errorf("博主 key=%s ordered=%v", s.WatermarkKey("1:0"), s.TimeOrdered("1:0"))
	}
}
