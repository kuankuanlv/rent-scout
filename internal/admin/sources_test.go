package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"rent-scout/internal/config"
)

// fakeController SourceController 测试替身：known 源清单 + enabled 状态 + 调用记录
type fakeController struct {
	known   map[string]bool
	enabled map[string]bool
	calls   []string // 记录动作调用（"name:action"）
}

func (f *fakeController) Sources() []string {
	names := make([]string, 0, len(f.known))
	for n := range f.known {
		names = append(names, n)
	}
	return names
}

func (f *fakeController) SetEnabled(name string, on bool) error {
	if !f.known[name] {
		return fmt.Errorf("未知源 %s", name)
	}
	action := "disable"
	if on {
		action = "enable"
	}
	f.calls = append(f.calls, name+":"+action)
	f.enabled[name] = on
	return nil
}

func (f *fakeController) Trigger(name string) error {
	if !f.known[name] {
		return fmt.Errorf("未知源 %s", name)
	}
	f.calls = append(f.calls, name+":trigger")
	return nil
}

func (f *fakeController) SourceEnabled(name string) bool {
	return f.enabled[name]
}

// TestAPISourcesList 源列表：200 含 name/enabled/cursor（游标来自 store.GetCursor）
func TestAPISourcesList(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if err := s.SetCursor("douban", "1:0"); err != nil {
		t.Fatal(err)
	}
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": false}}
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Sources []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Cursor  string `json:"cursor"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, rec.Body.String())
	}
	if len(out.Sources) != 1 || out.Sources[0].Name != "douban" ||
		out.Sources[0].Enabled != false || out.Sources[0].Cursor != "1:0" {
		t.Errorf("sources = %+v, want 1 条 douban disabled cursor=1:0", out.Sources)
	}
}

// TestAPISourceActions 源动作：POST trigger/enable/disable 调用替身方法；
// 未知源 404；路径/动作非法 400；GET 触发写操作 405
func TestAPISourceActions(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": true}}
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", ctrl)

	post := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	cases := []struct {
		path string
		want int
	}{
		{"/api/sources/douban/trigger", http.StatusOK},
		{"/api/sources/douban/enable", http.StatusOK},
		{"/api/sources/douban/disable", http.StatusOK},
		{"/api/sources/unknown/trigger", http.StatusNotFound},
		{"/api/sources/unknown/enable", http.StatusNotFound},
		{"/api/sources/unknown/disable", http.StatusNotFound},
		{"/api/sources/bad", http.StatusBadRequest},
		{"/api/sources/douban/bogus", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if code, _ := post(tc.path); code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.path, code, tc.want)
		}
	}

	// 替身被正确调用：trigger / enable / disable 各一次（未知源不记录）
	want := []string{"douban:trigger", "douban:enable", "douban:disable"}
	if len(ctrl.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", ctrl.calls, want)
	}
	for i, c := range want {
		if ctrl.calls[i] != c {
			t.Errorf("calls[%d] = %s, want %s", i, ctrl.calls[i], c)
		}
	}

	// GET 触发写操作 → 405（防 <a>/<img> 链接触发）
	req := httptest.NewRequest(http.MethodGet, "/api/sources/douban/trigger", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET trigger: status = %d, want 405", rec.Code)
	}
	// 列表 POST → 405
	req = httptest.NewRequest(http.MethodPost, "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("列表 POST: status = %d, want 405", rec.Code)
	}
}

// TestAPISourcesUnavailable ctrl nil（采集未启动）→ 列表与动作均 503
func TestAPISourcesUnavailable(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	for _, tc := range []struct {
		path string
		meth string
	}{
		{"/api/sources", http.MethodGet},
		{"/api/sources/douban/trigger", http.MethodPost},
	} {
		req := httptest.NewRequest(tc.meth, tc.path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", tc.path, rec.Code)
		}
	}
}

// TestAPISourcesAuth 鉴权开启下无 token → 401（/api/sources 不在豁免清单）
func TestAPISourcesAuth(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": true}}
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", ctrl)

	for _, path := range []string{"/api/sources", "/api/sources/douban/trigger"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 无 token: status = %d, want 401", path, rec.Code)
		}
	}
}