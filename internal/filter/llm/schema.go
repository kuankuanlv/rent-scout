package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/models"
)

// aiResultRaw LLM 输出单条（index 可缺省）
type aiResultRaw struct {
	Index      int
	Passed     bool
	Reason     string
	Price      int
	Contact    string
	Commuting  string
	Confidence float64
	Model      string
}

func (r *aiResultRaw) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Index = lookupInt(raw, "index")
	r.Passed = lookupBool(raw, "passed", "output", "is_real_rental", "is_real")
	r.Reason = lookupString(raw, "reason", "理由", "判断理由", "原因")
	r.Price = lookupInt(raw, "price", "价格", "租金", "月租", "月租金", "monthly_price", "monthly_rent", "rent_price", "rent")
	r.Contact = lookupString(raw, "contact", "联系人", "联系方式", "微信")
	r.Commuting = lookupString(raw, "commuting", "time", "通勤", "通勤时间")
	r.Confidence = lookupFloat(raw, "confidence")
	r.Model = lookupString(raw, "model")
	return nil
}

// ParseAIResults 解析批量判定输出：剥围栏、解包 {verdicts:[]}、抠 JSON 数组，再按 index 对齐。
func ParseAIResults(raw string, expected int) ([]models.AIResult, error) {
	cleaned := unwrapAIArray(raw)
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

func unwrapAIArray(raw string) string {
	s := stripJSONFence(raw)
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") {
		var obj struct {
			Verdicts json.RawMessage `json:"verdicts"`
			Results  json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			if len(obj.Verdicts) > 0 {
				return strings.TrimSpace(string(obj.Verdicts))
			}
			if len(obj.Results) > 0 {
				return strings.TrimSpace(string(obj.Results))
			}
		}
	}
	return extractJSONArray(s)
}

func extractJSONArray(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "[")
	if start < 0 {
		return s
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s
}

func lookupString(raw map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
	}
	return ""
}

func lookupInt(raw map[string]json.RawMessage, keys ...string) int {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				return n
			}
			var s string
			if json.Unmarshal(v, &s) == nil {
				n = 0
				for _, c := range s {
					if c >= '0' && c <= '9' {
						n = n*10 + int(c-'0')
					}
				}
				return n
			}
		}
	}
	return 0
}

func lookupFloat(raw map[string]json.RawMessage, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			var f float64
			if json.Unmarshal(v, &f) == nil {
				return f
			}
		}
	}
	return 0
}

func lookupBool(raw map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if v, ok := raw[k]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				return b
			}
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s == "true" || s == "True" || s == "TRUE"
			}
		}
	}
	return false
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

func hasIndex(r aiResultRaw) bool { return r.Index != 0 }

func isDuplicate(results []models.AIResult, idx int) bool {
	return results[idx].RawResponse != ""
}

func unmarshalAIResult(raw string, ai *models.AIResult) error {
	var r aiResultRaw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return err
	}
	*ai = models.AIResult{Passed: r.Passed, Reason: r.Reason, Price: r.Price,
		Contact: r.Contact, Commuting: r.Commuting, Confidence: r.Confidence, Model: r.Model, RawResponse: raw}
	return nil
}
