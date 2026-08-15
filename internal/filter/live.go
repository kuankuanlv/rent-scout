package filter

import (
	"rent-scout/internal/config"
	"rent-scout/internal/filter/llm"
)

func aiSwitchOn(app *config.AppConfig) bool {
	return app != nil && app.Filter.AIEnabled != nil && *app.Filter.AIEnabled
}

// LiveAIEvaluator 按当前热配置建评估器；未启用或没 Key 返回 nil 和原因
func LiveAIEvaluator(rt *config.HotConfig) (AIEvaluator, string) {
	if rt == nil {
		return nil, "当前配置 AI 未启用，无需执行"
	}
	app := rt.Get()
	if !aiSwitchOn(app) {
		return nil, "当前配置 AI 未启用，无需执行"
	}
	env := rt.Secrets()
	if env == nil || env.Filter.LLM.APIKey == "" {
		return nil, "当前配置 AI 密钥为空，无需执行"
	}
	baseURL := env.Filter.LLM.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := env.Filter.LLM.Model
	if model == "" {
		model = "deepseek-chat"
	}
	opts := []llm.ClientOptions{{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: model}}
	for _, m := range env.Filter.LLM.FallbackModels {
		opts = append(opts, llm.ClientOptions{BaseURL: baseURL, APIKey: env.Filter.LLM.APIKey, Model: m})
	}
	return NewAIBatchEvaluator(llm.NewPool(opts, llm.PoolOptions{})), ""
}
