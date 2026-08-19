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
	Layout     string
	RentType   string
	Floor      string
	Area       string
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
	r.Layout = lookupString(raw, "layout", "户型", "房型", "room_type")
	r.RentType = lookupString(raw, "rentType", "rent_type", "租赁方式", "整租合租")
	r.Floor = lookupString(raw, "floor", "楼层")
	r.Area = lookupString(raw, "area", "面积")
	r.Confidence = lookupFloat(raw, "confidence")
	r.Model = lookupString(raw, "model")
	return nil
}

// ParseAIResults 解析批量判定输出：剥围栏、解包 {verdicts:[]}、抠 JSON 数组，再按 index 对齐。
// 条数不足仍报错；多了（模型把一帖拆成多条）只保留 index∈[0,expected) 的先到项。
func ParseAIResults(raw string, expected int) ([]models.AIResult, error) {
	results, filled, err := ParseAIResultsPartial(raw, expected)
	if err != nil {
		return nil, err
	}
	if filled < expected {
		return nil, fmt.Errorf("AI 有效输出 %d < 预期 %d", filled, expected)
	}
	return results, nil
}

// ParseAIResultsPartial 截断的 JSON 也尽量留下完整对象；一个都抠不出才报错。
func ParseAIResultsPartial(raw string, expected int) ([]models.AIResult, int, error) {
	raws, err := parseAIResultRaws(raw)
	if err != nil {
		return nil, 0, err
	}
	results, filled := alignAIResults(raws, expected, raw)
	if filled == 0 {
		return nil, 0, fmt.Errorf("AI 有效输出 0 < 预期 %d", expected)
	}
	return results, filled, nil
}

func parseAIResultRaws(raw string) ([]aiResultRaw, error) {
	cleaned := unwrapAIArray(raw)
	var raws []aiResultRaw
	if err := json.Unmarshal([]byte(cleaned), &raws); err == nil {
		return raws, nil
	} else if salvaged := salvageArrayObjects(raw); len(salvaged) > 0 {
		return salvaged, nil
	} else {
		return nil, fmt.Errorf("AI 输出非 JSON 数组: %w", err)
	}
}

func alignAIResults(raws []aiResultRaw, expected int, raw string) ([]models.AIResult, int) {
	results := make([]models.AIResult, expected)
	filled := 0
	for i, r := range raws {
		idx := r.Index
		if r.Index == 0 && i != 0 && !hasIndex(raws[i]) {
			idx = i // 缺省 index：按顺序对齐
		}
		if idx < 0 || idx >= expected {
			continue // 多出来的丢弃
		}
		if isDuplicate(results, idx) {
			continue
		}
		results[idx] = models.AIResult{
			Passed: r.Passed, Reason: r.Reason, Price: r.Price,
			Contact: r.Contact, Commuting: r.Commuting,
			Layout: r.Layout, RentType: r.RentType, Floor: r.Floor, Area: r.Area,
			Confidence: r.Confidence, Model: r.Model, RawResponse: raw,
		}
		filled++
	}
	return results, filled
}

// salvageArrayObjects 从截断数组里用 decoder 逐个抠完整对象，尾巴丢掉
func salvageArrayObjects(raw string) []aiResultRaw {
	s := stripJSONFence(raw)
	start := strings.Index(s, "[")
	if start < 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil
	}
	var out []aiResultRaw
	for dec.More() {
		var r aiResultRaw
		if err := dec.Decode(&r); err != nil {
			break
		}
		out = append(out, r)
	}
	return out
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
		Contact: r.Contact, Commuting: r.Commuting,
		Layout: r.Layout, RentType: r.RentType, Floor: r.Floor, Area: r.Area,
		Confidence: r.Confidence, Model: r.Model, RawResponse: raw}
	return nil
}
