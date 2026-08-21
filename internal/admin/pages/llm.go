package pages

import (
	"context"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

// probeTimeout 探测类 handler 公共样板：POST 检查 + 12s 超时 ctx
func probeTimeout(r *http.Request) (context.Context, context.CancelFunc, bool) {
	if r.Method != http.MethodPost {
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	return ctx, cancel, true
}

// modelsPreview 模型列表预览：最多 8 个
func modelsPreview(models []string) []string {
	if len(models) > 8 {
		return models[:8]
	}
	return models
}

// handleLLMTest POST /admin/config/llm/test：草稿连通检测，不写库
func (h *ConfigHandler) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := probeTimeout(r)
	if !ok {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer cancel()
	draft, err := h.parseLLMDraft(r)
	if err != nil {
		ports.WriteJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// 轻量探测：优先 GET /models；失败再极短 chat
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测开始",
		"stage", "start",
		"base_url", draft.baseURL,
		"model", draft.model,
		"api_style", draft.apiStyle,
	)
	if h.opts.LLMProbe == nil {
		ports.WriteJSON(w, map[string]any{"ok": false, "error": "探测未配置"})
		return
	}
	models, listErr := h.opts.LLMProbe.ListModels(ctx, draft.baseURL, draft.apiKey, draft.model)
	if listErr == nil {
		pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
			"stage", "models",
			"ok", true,
			"base_url", draft.baseURL,
			"count", len(models),
			"preview", modelsPreview(models),
		)
		ports.WriteJSON(w, map[string]any{"ok": true, "detail": "models 接口可达", "via": "models", "count": len(models)})
		return
	}
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
		"stage", "models",
		"ok", false,
		"base_url", draft.baseURL,
		"err", listErr,
	)
	if draft.model == "" {
		ports.WriteJSON(w, map[string]any{"ok": false, "detail": "models 失败且未填模型: " + listErr.Error(), "error": listErr.Error()})
		return
	}
	reply, chatErr := h.opts.LLMProbe.Chat(ctx, draft.baseURL, draft.apiKey, draft.model, "ping", "回复 ok")
	if chatErr != nil {
		pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
			"stage", "chat",
			"ok", false,
			"base_url", draft.baseURL,
			"model", draft.model,
			"err", chatErr,
		)
		ports.WriteJSON(w, map[string]any{"ok": false, "detail": chatErr.Error(), "error": chatErr.Error(), "via": "chat"})
		return
	}
	pkglog.Component(pkglog.Admin).Info("LLM 连通检测",
		"stage", "chat",
		"ok", true,
		"base_url", draft.baseURL,
		"model", draft.model,
		"reply", clipLog(reply, 200),
	)
	ports.WriteJSON(w, map[string]any{"ok": true, "detail": "chat 通路正常", "via": "chat"})
}

// handleLLMModels POST /admin/config/llm/models：草稿拉取模型列表，不写库
func (h *ConfigHandler) handleLLMModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, ok := probeTimeout(r)
	if !ok {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer cancel()
	draft, err := h.parseLLMDraft(r)
	if err != nil {
		ports.WriteJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	pkglog.Component(pkglog.Admin).Info("拉取模型开始", "stage", "start", "base_url", draft.baseURL)
	if h.opts.LLMProbe == nil {
		ports.WriteJSON(w, map[string]any{"ok": false, "error": "探测未配置"})
		return
	}
	models, err := h.opts.LLMProbe.ListModels(ctx, draft.baseURL, draft.apiKey, draft.model)
	if err != nil {
		pkglog.Component(pkglog.Admin).Info("拉取模型失败", "stage", "models", "base_url", draft.baseURL, "err", err)
		ports.WriteJSON(w, map[string]any{"ok": false, "detail": err.Error(), "error": err.Error()})
		return
	}
	pkglog.Component(pkglog.Admin).Info("拉取模型成功", "stage", "models", "base_url", draft.baseURL, "count", len(models), "preview", modelsPreview(models))
	ports.WriteJSON(w, map[string]any{"ok": true, "models": models})
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

func (h *ConfigHandler) parseLLMDraft(r *http.Request) (llmDraft, error) {
	if err := r.ParseForm(); err != nil {
		return llmDraft{}, err
	}
	stored := h.opts.RT.Secrets().Filter.LLM
	style := strings.ToLower(strings.TrimSpace(ports.FirstNonEmpty(
		r.PostFormValue("api_style"),
		r.PostFormValue("secret.filter.llm.api_style"),
		stored.APIStyle,
	)))
	if style == "" {
		style = "openai"
	}
	baseURL := ports.FirstNonEmpty(
		r.PostFormValue("base_url"),
		r.PostFormValue("secret.filter.llm.base_url"),
	)
	if baseURL == "" {
		baseURL = stored.BaseURL
	}
	if baseURL == "" && style == "openai" {
		baseURL = config.DefaultLLMBaseURL
	}
	apiKey := ports.FirstNonEmpty(
		r.PostFormValue("api_key"),
		r.PostFormValue("secret.filter.llm.api_key"),
	)
	if apiKey == "" || apiKey == "••••••••" {
		apiKey = stored.APIKey
	}
	model := ports.FirstNonEmpty(
		r.PostFormValue("model"),
		r.PostFormValue("secret.filter.llm.model"),
		stored.Model,
	)
	if strings.TrimSpace(baseURL) == "" {
		return llmDraft{}, errLLMMissingBase
	}
	return llmDraft{apiStyle: style, baseURL: baseURL, apiKey: apiKey, model: model}, nil
}

type llmDraftError string

func (e llmDraftError) Error() string { return string(e) }

const errLLMMissingBase llmDraftError = "缺少 Base URL"
