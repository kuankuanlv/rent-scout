package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/pkglog"
)

// DefaultChatTimeout 本地网关首包可能很慢，60 秒经常还没等到响应头
const DefaultChatTimeout = 5 * time.Minute

// ClientOptions OpenAI 兼容客户端参数
type ClientOptions struct {
	BaseURL  string // 如 http://127.0.0.1:20128/v1（OpenAI 兼容）
	APIKey   string
	Model    string
	Client   *http.Client // 测试注入
	DumpHTTP bool         // 管理台探测：打 raw 请求应答
}

// Client LLM 客户端（规格 5.4）：POST {baseURL}/chat/completions
type Client struct {
	baseURL  string
	apiKey   string
	model    string
	http     *http.Client
	dumpHTTP bool
}

// NewClient 创建客户端
func NewClient(opts ClientOptions) *Client {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: DefaultChatTimeout}
	}
	return &Client{
		baseURL:  strings.TrimSuffix(opts.BaseURL, "/"),
		apiKey:   opts.APIKey,
		model:    opts.Model,
		http:     opts.Client,
		dumpHTTP: opts.DumpHTTP,
	}
}

// Model 客户端模型名（pool fallback 记录用）
func (c *Client) Model() string { return c.model }

// Chat 单次对话；返回 assistant 内容。非 2xx 返回错误（429/5xx 由 pool 处理 fallback）
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		c.dump("llm_chat", req, body, 0, nil, nil, err)
		return "", fmt.Errorf("LLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	c.dump("llm_chat", req, body, resp.StatusCode, resp.Header, b, nil)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	content, err := decodeChatContent(b)
	if err != nil {
		return "", err
	}
	return content, nil
}

// ListModels GET {baseURL}/models（OpenAI 兼容）；返回模型 id 列表
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		c.dump("llm_models", req, nil, 0, nil, nil, err)
		return nil, fmt.Errorf("LLM models 请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.dump("llm_models", req, nil, resp.StatusCode, resp.Header, b, nil)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM models HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("解析 models 响应: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		if id := strings.TrimSpace(d.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (c *Client) dump(stage string, req *http.Request, reqBody []byte, status int, respHeader http.Header, respBody []byte, err error) {
	if c == nil || !c.dumpHTTP || req == nil {
		return
	}
	if err != nil {
		pkglog.ProbeHTTPErr(pkglog.Admin, stage, req.Method, req.URL.String(), req.Header, err)
		return
	}
	pkglog.ProbeHTTP(pkglog.Admin, stage, req.Method, req.URL.String(), req.Header, reqBody, status, respHeader, respBody)
}

// truncate 错误消息截断（防日志膨胀）
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func decodeChatContent(b []byte) (string, error) {
	s := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	if strings.HasPrefix(s, "{") {
		out, err := contentFromChatJSON([]byte(s))
		if err == nil && strings.TrimSpace(out) != "" {
			return out, nil
		}
	}
	if looksLikeSSE(s) {
		out, err := decodeSSEContent(s)
		if err != nil {
			return "", fmt.Errorf("解析 LLM 响应: %w; body=%s", err, truncate(s, 120))
		}
		return out, nil
	}
	out, err := contentFromChatJSON([]byte(s))
	if err != nil {
		return "", fmt.Errorf("解析 LLM 响应: %w; body=%s", err, truncate(s, 120))
	}
	return out, nil
}

func looksLikeSSE(s string) bool {
	return strings.HasPrefix(s, "data:") || strings.Contains(s, "\ndata:")
}

func decodeSSEContent(s string) (string, error) {
	var deltas strings.Builder
	var lastFull string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		delta, full, err := contentsFromChatJSON([]byte(payload))
		if err != nil {
			continue
		}
		deltas.WriteString(delta)
		if full != "" {
			lastFull = full
		}
	}
	if out := deltas.String(); strings.TrimSpace(out) != "" {
		return out, nil
	}
	if strings.TrimSpace(lastFull) != "" {
		return lastFull, nil
	}
	return "", fmt.Errorf("SSE 无文本")
}

type chatChoice struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Delta struct {
		Content json.RawMessage `json:"content"`
	} `json:"delta"`
}

func contentFromChatJSON(b []byte) (string, error) {
	delta, full, err := contentsFromChatJSON(b)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(delta) != "" {
		return delta, nil
	}
	return full, nil
}

func contentsFromChatJSON(b []byte) (delta, full string, err error) {
	var out struct {
		Choices []chatChoice `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", "", err
	}
	if len(out.Choices) == 0 {
		return "", "", fmt.Errorf("无 choices")
	}
	c := out.Choices[0]
	if len(c.Delta.Content) > 0 && string(c.Delta.Content) != "null" {
		delta, err = stringifyContent(c.Delta.Content)
		if err != nil {
			return "", "", err
		}
	}
	if len(c.Message.Content) > 0 && string(c.Message.Content) != "null" {
		full, err = stringifyContent(c.Message.Content)
		if err != nil {
			return "", "", err
		}
	}
	return delta, full, nil
}

func stringifyContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("无法解析 content")
}
