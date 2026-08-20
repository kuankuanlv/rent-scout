package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

type fakeController struct {
	known   map[string]bool
	enabled map[string]bool
	calls   []string
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

func (f *fakeController) SourceEnabled(name string) bool {
	return f.enabled[name]
}

func TestAPISourcesList(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if err := s.SetProgress("douban", store.SourceProgress{Page: "1:0"}); err != nil {
		t.Fatal(err)
	}
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": false}}
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Sources []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Cursor  string `json:"cursor"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sources) != 1 || out.Sources[0].Name != "douban" ||
		out.Sources[0].Enabled || out.Sources[0].Cursor != "1:0" {
		t.Fatalf("sources = %+v", out.Sources)
	}
}

func TestAPISourceActions(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if err := s.SetProgress("douban", store.SourceProgress{Page: "1:0"}); err != nil {
		t.Fatal(err)
	}
	ctrl := &fakeController{known: map[string]bool{"douban": true}, enabled: map[string]bool{"douban": true}}
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", ctrl)

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post("/api/sources/douban/enable"); code != http.StatusOK {
		t.Fatalf("enable = %d", code)
	}
	if code := post("/api/sources/douban/disable"); code != http.StatusOK {
		t.Fatalf("disable = %d", code)
	}
	if code := post("/api/sources/douban/reset"); code != http.StatusOK {
		t.Fatalf("reset = %d", code)
	}
	if _, ok, err := s.GetCursor("douban"); err != nil || ok {
		t.Fatalf("reset 后仍有游标 ok=%v err=%v", ok, err)
	}
	if code := post("/api/sources/douban/trigger"); code != http.StatusBadRequest {
		t.Fatalf("trigger = %d, want 400", code)
	}
	if code := post("/api/sources/unknown/enable"); code != http.StatusNotFound {
		t.Fatalf("unknown = %d, want 404", code)
	}
}

func TestAPISourcesUnavailable(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
