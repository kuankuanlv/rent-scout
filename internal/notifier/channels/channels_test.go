package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rent-scout/internal/notifier/port"
)

// feishu：发送 post 富文本 JSON；返回 sent=全部 PostID
func TestFeishuChannelSend(t *testing.T) {
	var mu sync.Mutex
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = body
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ch := NewFeishuChannel(srv.URL)
	if ch.Name() != port.ChannelFeishu {
		t.Fatalf("name: %s", ch.Name())
	}
	items := []port.NotifyItem{{
		PostID: 1, Title: "望京整租", URL: "https://x", AddressTag: "望京",
		FeedbackURL: "https://fb/useful", FeedbackUselessURL: "https://fb/useless", HandledURL: "https://fb/h",
	}}
	sent, failed, err := ch.Send(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != 1 {
		t.Errorf("sent: %v", sent)
	}
	if len(failed) != 0 {
		t.Errorf("failed: %v", failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["msg_type"] != "post" {
		t.Errorf("msg_type: %v", got["msg_type"])
	}
	content := got["content"].(map[string]interface{})
	post := content["post"].(map[string]interface{})
	zh := post["zh_cn"].(map[string]interface{})
	if title, _ := zh["title"].(string); !strings.Contains(title, "望京") {
		t.Errorf("title: %v", zh["title"])
	}
	raw, _ := json.Marshal(zh["content"])
	blob := string(raw)
	if !strings.Contains(blob, "打开原帖") || !strings.Contains(blob, "https://x") {
		t.Errorf("content 缺原帖链接: %s", blob)
	}
	if !strings.Contains(blob, `"有用"`) || !strings.Contains(blob, "https://fb/useful") {
		t.Errorf("content 缺有用链接: %s", blob)
	}
}

// dingtalk：加签时 URL 带 timestamp/sign 参数
func TestDingtalkSignedURL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ch := NewDingtalkChannel(srv.URL, "secret")
	_, _, err := ch.Send(context.Background(), []port.NotifyItem{{PostID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotURL, "timestamp=") || !strings.Contains(gotURL, "sign=") {
		t.Errorf("签名参数缺失: %s", gotURL)
	}
}

// pushplus：token + title/content 表单
func TestPushplusChannelSend(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ch := NewPushplusChannel(srv.URL, "tok", "doubanzufang")
	_, _, err := ch.Send(context.Background(), []port.NotifyItem{{PostID: 1, Title: "t", AddressTag: "望京"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["token"] != "tok" {
		t.Errorf("token: %v", got["token"])
	}
	if got["topic"] != "doubanzufang" {
		t.Errorf("topic: %v", got["topic"])
	}
	if got["template"] != "html" {
		t.Errorf("template: %v", got["template"])
	}
	if !strings.Contains(got["content"], "AI审核") || !strings.Contains(got["content"], "有用") {
		t.Errorf("content: %v", got["content"])
	}
}

// 失败渠道：Send 返回 failed（非 2xx）
func TestChannelSendFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	ch := NewFeishuChannel(srv.URL)
	sent, failed, err := ch.Send(context.Background(), []port.NotifyItem{{PostID: 1}})
	if err == nil {
		t.Error("应返回 err")
	}
	if len(sent) != 0 || len(failed) != 1 {
		t.Errorf("sent=%v failed=%v", sent, failed)
	}
}
