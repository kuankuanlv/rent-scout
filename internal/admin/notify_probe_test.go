package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

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
	s := newAdminTestStore(t)
	defer s.Close()
	probe := &stubNotifyProbe{}
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
	if probe.channel != "feishu" || len(probe.items) != 2 {
		t.Fatalf("channel=%s items=%d", probe.channel, len(probe.items))
	}
	if !strings.Contains(probe.items[0].Title, "连通检测") {
		t.Errorf("样例标题: %s", probe.items[0].Title)
	}
}

func TestNotifyTestUsesRecentAnyStatus(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	p := models.RentPost{
		Source: "douban", ExternalID: "rej-1", Title: "被拒的帖子也能试发",
		URL: "https://example.com/p1", CollectedAt: time.Now(), Status: models.PostStatusRejected,
	}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	probe := &stubNotifyProbe{}
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
	if probe.channel != "pushplus" || len(probe.items) != 1 || probe.items[0].Title != "被拒的帖子也能试发" {
		t.Fatalf("items=%+v channel=%s", probe.items, probe.channel)
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
