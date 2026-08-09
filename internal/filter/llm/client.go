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
)

// ClientOptions OpenAI 兼容客户端参数
type ClientOptions struct {
	BaseURL string // 如 https://api.deepseek.com/v1（兼容 OpenAI 格式）
	APIKey  string
	Model   string
	Client  *http.Client // 测试注入
}

// Client LLM 客户端（规格 5.4）：POST {baseURL}/chat/completions
type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

// NewClient 创建客户端
func NewClient(opts ClientOptions) *Client {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{baseURL: strings.TrimSuffix(opts.BaseURL, "/"), apiKey: opts.APIKey, model: opts.Model, http: opts.Client}
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
		"temperature": 0, // 判定任务：确定性优先
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
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	// 解析 choices[0].message.content
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("解析 LLM 响应: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("LLM 响应无 choices")
	}
	return out.Choices[0].Message.Content, nil
}

// truncate 错误消息截断（防日志膨胀）
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
