package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 客户端：正确组装请求（model/messages/Authorization）并返回响应内容
func TestClientChat(t *testing.T) {
	var gotModel, gotAuth, gotSystem, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Temperature float64 `json:"temperature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		for _, m := range body.Messages {
			if m.Role == "system" {
				gotSystem = m.Content
			}
			if m.Role == "user" {
				gotUser = m.Content
			}
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"[{\"index\":0,\"passed\":true}]"}}]}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, APIKey: "sk-test", Model: "deepseek-chat"})
	out, err := c.Chat(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "deepseek-chat" || gotAuth != "Bearer sk-test" {
		t.Errorf("请求组装错误: model=%q auth=%q", gotModel, gotAuth)
	}
	if gotSystem != "sys" || gotUser != "usr" {
		t.Errorf("消息错误: %q %q", gotSystem, gotUser)
	}
	if !strings.Contains(out, "passed") {
		t.Errorf("响应内容错误: %s", out)
	}
}

// 非 2xx：报错
func TestClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("429 应报错")
	}
}
