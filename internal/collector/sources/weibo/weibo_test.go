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

const listPageHTML = `<html><body>
<div class="card-wrap" mid="0"><p class="txt">广告</p></div>
<div class="card-wrap" action-type="feed_list_item" mid="5123456789">
  <a class="name" nick-name="租房君">租房君</a>
  <p class="txt" node-type="feed_list_content">展开前摘要</p>
  <p class="txt" node-type="feed_list_content_full">望京整租两居 8000 微信 abc</p>
  <p class="from"><a href="//weibo.com/123/AbCdEf" title="2026-08-16 10:00">今天 10:00</a></p>
</div>
<div class="card-wrap" action-type="feed_list_item" mid="5123456790">
  <a class="name" nick-name="user2">user2</a>
  <p class="txt" node-type="feed_list_content">回龙观精装一居</p>
  <p class="from"><a href="/u/456" title="2026-08-15 09:00">昨天 09:00</a></p>
</div>
</body></html>`

type staticCookie string

func (s staticCookie) Get(context.Context, string) (string, error) { return string(s), nil }

func TestWeiboListParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/weibo" {
			t.Errorf("path=%s want /weibo", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("page=%s want 2", r.URL.Query().Get("page"))
		}
		if r.URL.Query().Get("q") != "#北京租房#" {
			t.Errorf("q=%s", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("typeall") != "1" || r.URL.Query().Get("suball") != "1" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		if r.URL.Query().Has("xsort") {
			t.Errorf("不应带 xsort，那是实时流不是高级搜索列表")
		}
		if !strings.HasPrefix(r.URL.Query().Get("timescope"), "custom:") {
			t.Errorf("timescope=%s", r.URL.Query().Get("timescope"))
		}
		if r.Header.Get("Cookie") == "" {
			t.Error("缺少 Cookie")
		}
		w.Write([]byte(listPageHTML))
	}))
	defer srv.Close()

	s := New(Options{
		Tags:   []string{"北京租房"},
		Base:   srv.URL,
		Cookie: staticCookie("SUB=x"),
		Client: srv.Client(),
	})
	items, next, err := s.List(context.Background(), "0:25")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	it := items[0]
	if it.ExternalID != "5123456789" || it.Author != "租房君" {
		t.Errorf("条目 %+v", it)
	}
	if !strings.Contains(it.Content, "望京整租两居") {
		t.Errorf("应取全文: %q", it.Content)
	}
	if it.NeedDetail {
		t.Error("有全文节点时不应再打详情")
	}
	if it.URL != "https://weibo.com/123/AbCdEf" {
		t.Errorf("url=%q", it.URL)
	}
	if it.PublishedAt.Format("2006-01-02 15:04") != "2026-08-16 10:00" {
		t.Errorf("time=%v", it.PublishedAt)
	}
	if next != "0:50" {
		t.Errorf("next=%q", next)
	}
}

func TestWeiboMultiURLRotation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "#空#" {
			w.Write([]byte(`<html><body><div class="card-wrap" mid="0"></div></body></html>`))
			return
		}
		w.Write([]byte(listPageHTML))
	}))
	defer srv.Close()
	s := New(Options{
		Tags:   []string{"空", "满"},
		Base:   srv.URL,
		Client: srv.Client(),
	})
	items, next, err := s.List(context.Background(), "")
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
	s := New(Options{Tags: []string{"x"}, Base: srv.URL, Client: srv.Client()})
	_, _, err := s.List(context.Background(), "")
	if !errors.Is(err, cookie.ErrCookieInvalid) {
		t.Fatalf("登录墙应判定 cookie 失效, err=%v", err)
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

func TestWeiboTruncatedListNeedsDetail(t *testing.T) {
	html := `<html><body>
<div class="card-wrap" action-type="feed_list_item" mid="5123456791">
  <a class="name" nick-name="租房君">租房君</a>
  <p class="txt" node-type="feed_list_content">望京摘要展开</p>
  <a action-type="fl_unfold" href="javascript:void(0);">展开</a>
  <p class="from"><a href="//weibo.com/1/x" title="2026-08-16 10:00">今天 10:00</a></p>
</div>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(html))
	}))
	defer srv.Close()
	s := New(Options{Tags: []string{"x"}, Base: srv.URL, Client: srv.Client()})
	items, _, err := s.List(context.Background(), "")
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if !items[0].NeedDetail {
		t.Fatal("截断摘要应 NeedDetail")
	}
	if strings.Contains(items[0].Content, "展开") {
		t.Errorf("摘要不应残留展开: %q", items[0].Content)
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

func TestWeiboTimescope(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 16, 0, 0, 0, 0, time.Local)
	got := weiboTimescope(start, end)
	if got != "custom:2026-08-01-0:2026-08-16-0" {
		t.Errorf("timescope=%s", got)
	}
	u, err := advancedSearchURL("https://s.weibo.com/weibo", "#北京租房#", start, end, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u, "q=#") || strings.Contains(u, "#北京") {
		t.Fatalf("q 未编码，浏览器会把 # 当锚点: %s", u)
	}
	if !strings.Contains(u, "q=%23") || !strings.Contains(u, "timescope=custom") {
		t.Errorf("url=%s", u)
	}
	if strings.Contains(u, "xsort=") {
		t.Errorf("不应带 xsort: %s", u)
	}
}

func TestWeiboDescribeCursor(t *testing.T) {
	s := New(Options{Tags: []string{"#北京租房#", "#北京合租#"}})
	got := s.DescribeCursor("")
	if !strings.Contains(got, "搜索1") || !strings.Contains(got, "北京租房") || !strings.Contains(got, "第1页") {
		t.Errorf("空游标 = %s", got)
	}
	got = s.DescribeCursor("1:25")
	if !strings.Contains(got, "搜索2") || !strings.Contains(got, "北京合租") || !strings.Contains(got, "第2页") {
		t.Errorf("1:25 = %s", got)
	}
}

func TestParseWeiboTime(t *testing.T) {
	got, err := parseWeiboTime("2026-08-16 10:00", "今天 10:00")
	if err != nil || got.Format("15:04") != "10:00" {
		t.Fatalf("%v %v", got, err)
	}
}
