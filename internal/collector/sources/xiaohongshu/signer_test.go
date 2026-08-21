package xiaohongshu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPSigner(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/sign" {
				t.Errorf("expected /sign, got %s", r.URL.Path)
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(SignResult{XS: "s", XT: "t"})
		}))
		defer ts.Close()

		signer, _ := NewHTTPSigner(ts.URL, nil)
		res, err := signer.Sign(context.Background(), SignRequest{Method: "GET", Path: "/test"})
		if err != nil {
			t.Fatal(err)
		}
		if res.XS != "s" || res.XT != "t" {
			t.Errorf("unexpected result: %+v", res)
		}
	})

	t.Run("non 2xx status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		signer, _ := NewHTTPSigner(ts.URL, nil)
		_, err := signer.Sign(context.Background(), SignRequest{})
		if err == nil {
			t.Error("expected error for non 2xx status")
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(SignResult{XS: ""}) // Missing XT
		}))
		defer ts.Close()

		signer, _ := NewHTTPSigner(ts.URL, nil)
		_, err := signer.Sign(context.Background(), SignRequest{})
		if err == nil {
			t.Error("expected error for missing sign fields")
		}
	})

	t.Run("timeout/cancellation", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		signer, _ := NewHTTPSigner(ts.URL, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := signer.Sign(ctx, SignRequest{})
		if err == nil {
			t.Error("expected timeout error")
		}
	})
}
