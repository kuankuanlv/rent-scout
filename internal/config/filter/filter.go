package filter

import "strings"

type Config struct {
	AIEnabled   *bool
	BatchSize   int
	AIBatchSize int
}

type SecretsFilter struct {
	LLM LLMConfig
}

type LLMConfig struct {
	APIKey         string
	BaseURL        string
	Model          string
	FallbackModels []string
	APIStyle       string
}

type LLMAPIStyle string

const (
	LLMStyleNone   LLMAPIStyle = "none"
	LLMStyleOpenAI LLMAPIStyle = "openai"
	LLMStyleOther  LLMAPIStyle = "other"
)

func (s LLMAPIStyle) String() string { return string(s) }

func ParseLLMAPIStyle(s string) LLMAPIStyle {
	s = strings.ToLower(strings.TrimSpace(s))
	switch LLMAPIStyle(s) {
	case LLMStyleNone, LLMStyleOpenAI, LLMStyleOther:
		return LLMAPIStyle(s)
	default:
		return LLMAPIStyle(s)
	}
}

const (
	DefaultLLMBaseURL = "https://api.deepseek.com"
	DefaultLLMModel   = "deepseek-chat"
)

func ApplyDefaults(c *Config) {
	if c.BatchSize == 0 {
		c.BatchSize = 20
	}
	if c.AIBatchSize == 0 {
		c.AIBatchSize = 10
	}
	if c.AIEnabled == nil {
		enabled := true
		c.AIEnabled = &enabled
	}
}
