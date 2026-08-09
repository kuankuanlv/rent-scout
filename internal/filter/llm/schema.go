package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/models"
)

// aiResultRaw LLM 输出单条（index 可缺省）
type aiResultRaw struct {
	Index      int     `json:"index"`
	Passed     bool    `json:"passed"`
	Reason     string  `json:"reason"`
	Price      int     `json:"price"`
	Contact    string  `json:"contact"`
	Commuting  string  `json:"commuting"`
	Confidence float64 `json:"confidence"`
	Model      string  `json:"model"`
}

// ParseAIResults 解析批量判定输出（规格 5.4）：剥离 ```json 围栏 → 解析数组 →
// index 对齐（缺失时按顺序补齐）。数量不匹配/重复 index → 报错（宁可整批重试不误标）
func ParseAIResults(raw string, expected int) ([]models.AIResult, error) {
	cleaned := stripJSONFence(raw)
	var raws []aiResultRaw
	if err := json.Unmarshal([]byte(cleaned), &raws); err != nil {
		return nil, fmt.Errorf("AI 输出非 JSON 数组: %w", err)
	}
	if len(raws) != expected {
		return nil, fmt.Errorf("AI 输出数量 %d != 预期 %d", len(raws), expected)
	}
	results := make([]models.AIResult, expected)
	for i, r := range raws {
		idx := r.Index
		if r.Index == 0 && i != 0 && !hasIndex(raws[i]) {
			idx = i // 缺省 index：按顺序对齐
		}
		if idx < 0 || idx >= expected {
			return nil, fmt.Errorf("AI 输出 index %d 越界", idx)
		}
		if isDuplicate(results, idx) {
			return nil, fmt.Errorf("AI 输出 index %d 重复", idx)
		}
		results[idx] = models.AIResult{
			Passed: r.Passed, Reason: r.Reason, Price: r.Price,
			Contact: r.Contact, Commuting: r.Commuting, Confidence: r.Confidence,
			Model: r.Model, RawResponse: raw,
		}
	}
	return results, nil
}

// stripJSONFence 剥离 ```json ... ``` 围栏（LLM 常见输出）
func stripJSONFence(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// hasIndex 该条是否显式携带 index 字段
func hasIndex(r aiResultRaw) bool { return r.Index != 0 }

// isDuplicate 重复 index 检测：除首条外 index 已在前面出现过
func isDuplicate(results []models.AIResult, idx int) bool {
	return results[idx].RawResponse != ""
}

// unmarshalAIResult 单条解析（测试辅助）
func unmarshalAIResult(raw string, ai *models.AIResult) error {
	var r aiResultRaw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return err
	}
	*ai = models.AIResult{Passed: r.Passed, Reason: r.Reason, Price: r.Price,
		Contact: r.Contact, Commuting: r.Commuting, Confidence: r.Confidence, Model: r.Model, RawResponse: raw}
	return nil
}
