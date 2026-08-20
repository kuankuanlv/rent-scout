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
			Temperature    float64        `json:"temperature"`
			Stream         *bool          `json:"stream"`
			ResponseFormat map[string]any `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotModel = body.Model
		if body.Stream != nil && *body.Stream {
			t.Errorf("未要求流式时不应发 stream=true")
		}
		if body.ResponseFormat != nil {
			t.Errorf("不应带 response_format，以免网关卡住: %v", body.ResponseFormat)
		}
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

func TestClientChatSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"verdicts\\\":\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"[]}\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Model: "oc-chat"})
	out, err := c.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"verdicts":[]}` {
		t.Errorf("SSE 拼接错误: %q", out)
	}
}

func TestClientChatSSEMessageContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"message\":{\"content\":\"{\\\"verdicts\\\":[]}\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Model: "oc-chat"})
	out, err := c.Chat(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"verdicts":[]}` {
		t.Errorf("SSE message.content = %q", out)
	}
}
