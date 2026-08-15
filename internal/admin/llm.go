package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/filter/llm"
	"rent-scout/internal/pkglog"
)

// handleLLMTest POST /admin/config/llm/test：草稿连通检测，不写库
func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	draft, err := s.parseLLMDraft(r)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	// 轻量探测：优先 GET /models；失败再极短 chat
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测开始",
		"stage", "start",
		"base_url", draft.baseURL,
		"model", draft.model,
		"api_style", draft.apiStyle,
	)
	c := llm.NewClient(llm.ClientOptions{BaseURL: draft.baseURL, APIKey: draft.apiKey, Model: draft.model, DumpHTTP: true})
	models, listErr := c.ListModels(ctx)
	if listErr == nil {
		preview := models
		if len(preview) > 8 {
			preview = preview[:8]
		}
		pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
			"stage", "models",
			"ok", true,
			"base_url", draft.baseURL,
			"count", len(models),
			"preview", preview,
		)
		writeJSON(w, map[string]any{"ok": true, "detail": "models 接口可达", "via": "models", "count": len(models)})
		return
	}
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
		"stage", "models",
		"ok", false,
		"base_url", draft.baseURL,
		"err", listErr,
	)
	if draft.model == "" {
		writeJSON(w, map[string]any{"ok": false, "detail": "models 失败且未填模型: " + listErr.Error(), "error": listErr.Error()})
		return
	}
	reply, chatErr := c.Chat(ctx, "ping", "回复 ok")
	if chatErr != nil {
		pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
			"stage", "chat",
			"ok", false,
			"base_url", draft.baseURL,
			"model", draft.model,
			"err", chatErr,
		)
		writeJSON(w, map[string]any{"ok": false, "detail": chatErr.Error(), "error": chatErr.Error(), "via": "chat"})
		return
	}
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
		"stage", "chat",
		"ok", true,
		"base_url", draft.baseURL,
		"model", draft.model,
		"reply", clipLog(reply, 200),
	)
	writeJSON(w, map[string]any{"ok": true, "detail": "chat 通路正常", "via": "chat"})
}

// handleLLMModels POST /admin/config/llm/models：草稿拉取模型列表，不写库
func (s *Server) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	draft, err := s.parseLLMDraft(r)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	pkglog.Component(pkglog.Admin).Info("拉取模型开始", "stage", "start", "base_url", draft.baseURL)
	c := llm.NewClient(llm.ClientOptions{BaseURL: draft.baseURL, APIKey: draft.apiKey, Model: draft.model, DumpHTTP: true})
	models, err := c.ListModels(ctx)
	if err != nil {
		pkglog.Component(pkglog.Admin).Info("拉取模型失败", "stage", "models", "base_url", draft.baseURL, "err", err)
		writeJSON(w, map[string]any{"ok": false, "detail": err.Error(), "error": err.Error()})
		return
	}
	preview := models
	if len(preview) > 8 {
		preview = preview[:8]
	}
	pkglog.Component(pkglog.Admin).Info("拉取模型成功", "stage", "models", "base_url", draft.baseURL, "count", len(models), "preview", preview)
	writeJSON(w, map[string]any{"ok": true, "models": models})
}

func clipLog(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

type llmDraft struct {
	apiStyle string
	baseURL  string
	apiKey   string
	model    string
}

func (s *Server) parseLLMDraft(r *http.Request) (llmDraft, error) {
	if err := r.ParseForm(); err != nil {
		return llmDraft{}, err
	}
	stored := s.rt.Secrets().Filter.LLM
	style := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		r.PostFormValue("api_style"),
		r.PostFormValue("secret.filter.llm.api_style"),
		stored.APIStyle,
	)))
	if style == "" {
		style = "openai"
	}
	baseURL := firstNonEmpty(
		r.PostFormValue("base_url"),
		r.PostFormValue("secret.filter.llm.base_url"),
	)
	if baseURL == "" {
		baseURL = stored.BaseURL
	}
	if baseURL == "" && style == "openai" {
		baseURL = "https://api.deepseek.com"
	}
	apiKey := firstNonEmpty(
		r.PostFormValue("api_key"),
		r.PostFormValue("secret.filter.llm.api_key"),
	)
	if apiKey == "" || apiKey == "••••••••" {
		apiKey = stored.APIKey
	}
	model := firstNonEmpty(
		r.PostFormValue("model"),
		r.PostFormValue("secret.filter.llm.model"),
		stored.Model,
	)
	if strings.TrimSpace(apiKey) == "" {
		return llmDraft{}, errLLMMissingKey
	}
	if strings.TrimSpace(baseURL) == "" {
		return llmDraft{}, errLLMMissingBase
	}
	return llmDraft{apiStyle: style, baseURL: baseURL, apiKey: apiKey, model: model}, nil
}

type llmDraftError string

func (e llmDraftError) Error() string { return string(e) }

const (
	errLLMMissingKey  llmDraftError = "缺少 API Key（草稿或已存）"
	errLLMMissingBase llmDraftError = "缺少 Base URL"
)
