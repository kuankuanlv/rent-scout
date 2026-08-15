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

// ClientOptions OpenAI 兼容客户端参数
type ClientOptions struct {
	BaseURL  string // 如 https://api.deepseek.com/v1（兼容 OpenAI 格式）
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
		opts.Client = &http.Client{Timeout: 60 * time.Second}
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
		"response_format": batchVerdictSchema(),
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

// batchVerdictSchema 约束批量判定 JSON（Structured Outputs）；顶层必须是 object。
func batchVerdictSchema() map[string]any {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index":      map[string]any{"type": "integer", "description": "与输入第几条对应，从 0 开始"},
			"passed":     map[string]any{"type": "boolean", "description": "是否通过用户规则"},
			"reason":     map[string]any{"type": "string", "description": "判定理由，中文 30 字内"},
			"price":      map[string]any{"type": "integer", "description": "月租金，单位元。如月租3000填3000，房租2500/月填2500，2000-2500填2000。仅数字。未明确提及填0"},
			"contact":    map[string]any{"type": "string", "description": "微信/手机号等原文，未提及填空串"},
			"commuting":  map[string]any{"type": "string", "description": "通勤/交通原文关键词，未提及填空串"},
			"confidence": map[string]any{"type": "number", "description": "0 到 1"},
		},
		"required":             []string{"index", "passed", "reason", "price", "contact", "commuting", "confidence"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "batch_ai_verdict",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdicts": map[string]any{
						"type":  "array",
						"items": item,
					},
				},
				"required":             []string{"verdicts"},
				"additionalProperties": false,
			},
		},
	}
}
