package channels

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

func postJSON(ctx context.Context, url string, body []byte, timeoutSec int) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	c := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return "", fmt.Errorf("读响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("webhook %d: %s", resp.StatusCode, truncateRunes(string(b), 200))
	}
	return string(b), nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
