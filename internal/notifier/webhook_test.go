package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 成功：2xx + 响应体返回
func TestPostJSONOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type: %s", ct)
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	body := []byte(`{"text":"hi"}`)
	resp, err := PostJSON(context.Background(), srv.URL, body, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "ok") {
		t.Errorf("响应: %s", resp)
	}
}

// 非 2xx：返回错误（含状态码与截断响应体）
func TestPostJSONNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()
	_, err := PostJSON(context.Background(), srv.URL, []byte(`{}`), 5)
	if err == nil {
		t.Fatal("403 应报错")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("错误应含状态码: %v", err)
	}
}

// 网络错误：返回错误
func TestPostJSONNetworkErr(t *testing.T) {
	_, err := PostJSON(context.Background(), "http://127.0.0.1:1", []byte(`{}`), 1)
	if err == nil {
		t.Fatal("网络错误应报错")
	}
}
