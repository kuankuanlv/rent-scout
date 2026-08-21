package ai

import (
	"rent-scout/internal/config"
	"rent-scout/internal/filter/ai/llm"
	"rent-scout/internal/filter/rule"
)

func aiSwitchOn(app *config.AppConfig) bool {
	return app != nil && app.Filter.AIEnabled != nil && *app.Filter.AIEnabled
}

// LiveAIEvaluator 按当前热配置建评估器；未启用或没 Key 返回 nil 和原因
func LiveAIEvaluator(rt *config.HotConfig) (rule.AIEvaluator, string) {
	if rt == nil {
		return nil, "当前配置 AI 未启用，无需执行"
	}
	app := rt.Get()
	if !aiSwitchOn(app) {
		return nil, "当前配置 AI 未启用，无需执行"
	}
	env := rt.Secrets()
	baseURL := env.Filter.LLM.BaseURL
	if baseURL == "" {
		baseURL = config.DefaultLLMBaseURL
	}
	model := env.Filter.LLM.Model
	if model == "" {
		model = config.DefaultLLMModel
	}
	opts := []llm.ClientOptions{{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: model}}
	for _, m := range env.Filter.LLM.FallbackModels {
		opts = append(opts, llm.ClientOptions{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: m})
	}
	return NewAIBatchEvaluator(llm.NewPool(opts, llm.PoolOptions{})), ""
}
