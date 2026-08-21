package xiaohongshu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"rent-scout/internal/config"
	"rent-scout/internal/collector/cookie"
	"errors"
)

func TestXiaohongshuIntegration(t *testing.T) {
	t.Run("DefaultDisabled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("Should not have received any request, but got %s", r.URL.Path)
		}))
		defer server.Close()

		s := New(Options{SearchURL: server.URL})
		
		targets := s.targets()
		if len(targets) != 0 {
			t.Errorf("Expected 0 targets, got %d", len(targets))
		}
	})

	t.Run("EnabledListParsing", func(t *testing.T) {
		app := config.DefaultApp()
		app.Collector.Xiaohongshu.Searches = []string{"test-keyword"}
		cfg := config.NewHotConfigWithSnapshot(app, nil)
		
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"success\":true,\"data\":{\"items\":[]}}"))
		}))
		defer server.Close()

		s := New(Options{Config: cfg, SearchURL: server.URL})
		targets := s.targets()
		
		if len(targets) == 0 {
			t.Fatal("Expected targets to be enabled, but got none")
		}

		_, err := s.searchNotes(context.Background(), targets[0], 1)
		if err != nil {
			t.Errorf("Expected success, got %v", err)
		}
	})

	t.Run("RiskResponse", func(t *testing.T) {
		app := config.DefaultApp()
		app.Collector.Xiaohongshu.Searches = []string{"test-keyword"}
		cfg := config.NewHotConfigWithSnapshot(app, nil)
		
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(461)
		}))
		defer server.Close()

		s := New(Options{Config: cfg, SearchURL: server.URL})
		targets := s.targets()
		
		_, err := s.searchNotes(context.Background(), targets[0], 1)
		if err == nil {
			t.Errorf("Expected error on 461, got nil")
		}
		if !errors.Is(err, cookie.ErrCookieInvalid) {
			t.Errorf("Expected ErrCookieInvalid, got %v", err)
		}
	})
}
