package xiaohongshu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type SignRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

type SignResult struct {
	XS       string `json:"x-s"`
	XT       string `json:"x-t"`
	XSCommon string `json:"x-s-common"`
}

type Signer interface {
	Sign(context.Context, SignRequest) (SignResult, error)
}

type httpSigner struct {
	addr   string
	client *http.Client
}

func NewHTTPSigner(addr string, client *http.Client) (Signer, error) {
	if client == nil {
		client = http.DefaultClient
	}
	return &httpSigner{
		addr:   strings.TrimSuffix(addr, "/"),
		client: client,
	}, nil
}

func (s *httpSigner) Sign(ctx context.Context, req SignRequest) (SignResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return SignResult{}, fmt.Errorf("marshal request: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, s.addr+"/sign", bytes.NewBuffer(body))
	if err != nil {
		return SignResult{}, fmt.Errorf("create request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(r)
	if err != nil {
		return SignResult{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SignResult{}, fmt.Errorf("signer return status: %d", resp.StatusCode)
	}

	var res SignResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return SignResult{}, fmt.Errorf("decode response: %w", err)
	}

	if res.XS == "" || res.XT == "" {
		return SignResult{}, errors.New("incomplete sign result: missing x-s or x-t")
	}

	return res, nil
}
