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
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, time.Local)
	it := s.NewIterator("", start, end)
	if it.Next(context.Background()) {
		t.Fatal("登录墙不应产出条目")
	}
	if !errors.Is(it.Err(), cookie.ErrCookieInvalid) {
		t.Fatalf("登录墙应判定 cookie 失效, err=%v", it.Err())
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
}
