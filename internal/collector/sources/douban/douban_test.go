package douban

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
)

// 豆瓣讨论列表页 fixture（参照参考仓库 service_test.go 结构）
const listPageHTML = `<html><head><title>北京租房</title></head><body>
<table class="olt">
<tr><th>title</th><th>author</th><th>reply</th><th>time</th></tr>
<tr>
  <td class="title"><a href="https://www.douban.com/group/topic/111/" title="望京整租两居"></a></td>
  <td><a href="https://www.douban.com/people/user1/">user1</a></td>
  <td class="r-count">3</td>
  <td class="time">2026-08-06 10:00</td>
</tr>
<tr>
  <td class="title"><a href="https://www.douban.com/group/topic/222/" title="回龙观精装一居"></a></td>
  <td><a href="https://www.douban.com/people/user2/">user2</a></td>
  <td class="r-count">0</td>
  <td class="time">08-05 09:00</td>
</tr>
</table></body></html>`

// 列表页解析：条目字段完整（标题/链接/作者/时间/ID）；游标格式 "组下标:偏移"
func TestDoubanListParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求带 start 参数与 cookie
		if !strings.Contains(r.URL.RawQuery, "start=25") {
			t.Errorf("请求缺少 start=25: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Cookie") == "" {
			t.Error("请求缺少 Cookie 头")
		}
		w.Write([]byte(listPageHTML))
	}))
	defer srv.Close()

	d, err := NewDouban(DoubanOptions{
		GroupURLs: []string{srv.URL + "/group/35417/discussion"},
		Cookie:    staticCookie("test-cookie"),
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	items, next, err := d.List(context.Background(), "0:25")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("条目数 = %d, want 2", len(items))
	}
	it := items[0]
	if it.ExternalID != "111" || it.URL != "https://www.douban.com/group/topic/111/" ||
		it.Title != "望京整租两居" || it.Author != "user1" {
		t.Errorf("条目字段错误: %+v", it)
	}
	if it.PublishedAt.IsZero() {
		t.Error("时间未解析")
	}
	if next != "0:50" {
		t.Errorf("next = %q, want 0:50", next)
	}
}

// 多小组轮转：当前组无条目 → 游标推进到下一组；最后一组结束返回 ""
func TestDoubanMultiGroupRotation(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "empty-group") {
			w.Write([]byte(`<html><table class="olt"><tr><th>t</th></tr></table></html>`))
			return
		}
		w.Write([]byte(listPageHTML))
	}))
	defer srv.Close()

	d, _ := NewDouban(DoubanOptions{
		GroupURLs: []string{srv.URL + "/group/empty-group/discussion", srv.URL + "/group/35417/discussion"},
		Client:    srv.Client(),
	})
	// 组 0 空页 → next 应推进到组 1
	items, next, err := d.List(context.Background(), "0:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("空组应有 0 条: %d", len(items))
	}
	if next != "1:0" {
		t.Errorf("空组 next = %q, want 1:0", next)
	}
	// 组 1 有条目 → 同组下一页（offset+25，从 0 起步）
	items, next, err = d.List(context.Background(), "1:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || next != "1:25" {
		t.Errorf("组1 解析错误: items=%d next=%q", len(items), next)
	}
	// 越界组 → 结束
	_, next, err = d.List(context.Background(), "2:0")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Errorf("越界组 next = %q, want 空（结束）", next)
	}
}

func TestDoubanSkipGroup(t *testing.T) {
	d, err := NewDouban(DoubanOptions{GroupURLs: []string{"http://g0/x", "http://g1/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.SkipGroup(""); got != "1:0" {
		t.Errorf("SkipGroup(\"\") = %q, want 1:0", got)
	}
	if got := d.SkipGroup("0:50"); got != "1:0" {
		t.Errorf("SkipGroup(0:50) = %q, want 1:0", got)
	}
	if got := d.SkipGroup("1:0"); got != "" {
		t.Errorf("最后一组 SkipGroup = %q, want 空", got)
	}
}

// 风控检测：响应含风控关键字 → 报错（参考仓库 detail.go 检测逻辑）
func TestDoubanRiskDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>检测到有异常请求，请稍后重试</html>`))
	}))
	defer srv.Close()
	d, _ := NewDouban(DoubanOptions{GroupURLs: []string{srv.URL + "/x"}, Client: srv.Client()})
	if _, _, err := d.List(context.Background(), ""); err == nil {
		t.Fatal("风控响应应报错")
	}
}

// staticCookie 测试辅助：固定 cookie 的提供器
type staticCookie string

func (c staticCookie) Get(ctx context.Context, source string) (string, error) {
	return string(c), nil
}

const detailPageHTML = `<html><head><title>望京整租两居</title></head><body>
<div class="topic-content">
  <p>望京西园四区，两居整租，月租 4500，近 14 号线望京站，<img src="https://img.example.com/1.jpg"></p>
</div>
<div class="from">
  <a href="https://www.douban.com/people/user1/">user1</a>
</div>
<span class="create-time">2026-08-06 10:00:00</span>
</body></html>`

// 详情页归一化：RentPost 字段完整（标题/正文/作者/ID/源标识）
func TestDoubanDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(detailPageHTML))
	}))
	defer srv.Close()
	d, _ := NewDouban(DoubanOptions{GroupURLs: []string{srv.URL + "/x"}, Client: srv.Client()})

	item := collector.ListItem{ExternalID: "111", URL: srv.URL + "/group/topic/111/",
		Title: "望京整租两居", Author: "user1", PublishedAt: time.Now()}
	post, err := d.Detail(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if post.Source != "douban" || post.ExternalID != "111" {
		t.Errorf("源标识错误: %+v", post)
	}
	if !strings.Contains(post.Content, "望京西园四区") || !strings.Contains(post.Content, "4500") {
		t.Errorf("正文缺失: %s", post.Content)
	}
	// 图片链接保留（正文 HTML 含 img）
	if !strings.Contains(post.Content, "img.example.com") {
		t.Error("正文应保留图片链接")
	}
	if post.Title != "望京整租两居" || post.Author != "user1" {
		t.Errorf("标题/作者错误: %+v", post)
	}
	// Raw 保留原文（重放/排查，规格 3.1）
	if !strings.Contains(post.Raw, "topic-content") {
		t.Error("Raw 应保留原始 HTML")
	}
}

// 真实豆瓣链接带 _spm_id 查询串：topicID 提取应忽略查询串（冒烟验证发现）
func TestTopicIDFromURLWithQuery(t *testing.T) {
	id := topicIDFromURL("https://www.douban.com/group/topic/496325305/?_spm_id=MTk3MDc1NDk3")
	if id != "496325305" {
		t.Errorf("topicIDFromURL = %q, want 496325305", id)
	}
}

func TestGroupsFollowHotConfig(t *testing.T) {
	app := &config.AppConfig{Collector: config.CollectorConfig{Douban: config.DoubanConfig{Groups: []string{"http://g/a"}}}}
	rt := config.NewHotConfigWithSnapshot(app, nil)
	d, err := NewDouban(DoubanOptions{Config: rt})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.groups(); len(got) != 1 || got[0] != "http://g/a" {
		t.Fatalf("got %v", got)
	}
	app.Collector.Douban.Groups = []string{"http://g/b", "http://g/c"}
	if len(d.groups()) != 2 {
		t.Fatalf("热更新后 %v", d.groups())
	}
}
