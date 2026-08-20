package douban

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/collector"
)

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

func TestDoubanListParse(t *testing.T) {
	items, err := ParseList(listPageHTML)
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
}

func TestDoubanRiskDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>检测到有异常请求，请稍后重试</html>`))
	}))
	defer srv.Close()
	d, _ := NewDouban(DoubanOptions{GroupURLs: []string{srv.URL + "/x"}, Client: srv.Client()})
	if _, err := d.get(context.Background(), srv.URL+"/x"); err == nil {
		t.Fatal("风控响应应报错")
	}
}

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
	if strings.Contains(post.Content, "img.example.com") || strings.Contains(post.Content, "<img") {
		t.Error("正文不应保留图片")
	}
}

func TestTopicIDFromURLWithQuery(t *testing.T) {
	id := topicIDFromURL("https://www.douban.com/group/topic/496325305/?_spm_id=MTk3MDc1NDk3")
	if id != "496325305" {
		t.Errorf("topicIDFromURL = %q, want 496325305", id)
	}
}
