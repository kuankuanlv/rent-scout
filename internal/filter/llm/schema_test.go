package llm

import (
	"strings"
	"testing"

	"rent-scout/internal/models"
)

// 解析合法输出：数组 + index 对齐
func TestParseAIResults(t *testing.T) {
	raw := `[{"index":0,"passed":true,"reason":"近地铁","price":4500,"contact":"张先生","commuting":"步行5分钟","confidence":0.9},
	         {"index":1,"passed":false,"reason":"隔断","price":0,"confidence":0.8}]`
	results, err := ParseAIResults(raw, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Passed != true || results[1].Passed != false {
		t.Errorf("解析错误: %+v", results)
	}
	if results[0].Price != 4500 || results[0].Contact != "张先生" {
		t.Errorf("字段抽取错误: %+v", results[0])
	}
}

func TestParseAIResultsWrappedVerdicts(t *testing.T) {
	raw := `{"verdicts":[{"index":0,"passed":true,"price":"月租3800","reason":"ok"}]}`
	results, err := ParseAIResults(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Price != 3800 || !results[0].Passed {
		t.Errorf("解包 verdicts 失败: %+v", results[0])
	}
}

// 解析容错：LLM 常带 ```json 代码块围栏，需剥离
func TestParseAIResultsWithFence(t *testing.T) {
	raw := "```json\n[{\"index\":0,\"passed\":true}]\n```"
	results, err := ParseAIResults(raw, 1)
	if err != nil {
		t.Fatalf("围栏剥离失败: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Errorf("解析错误: %+v", results)
	}
}

// 缺失 index：按顺序补齐（容错）；index 越界/重复：报错
func TestParseAIResultsIndexTolerance(t *testing.T) {
	// 缺失 index → 按顺序对齐
	raw := `[{"passed":true},{"passed":false}]`
	results, err := ParseAIResults(raw, 2)
	if err != nil || len(results) != 2 || results[1].Passed {
		t.Errorf("缺 index 容错失败: %+v %v", results, err)
	}
	// 数量不匹配：报错（对齐失败宁可整批重试）
	raw2 := `[{"index":0,"passed":true}]`
	if _, err := ParseAIResults(raw2, 3); err == nil {
		t.Error("数量不匹配应报错")
	}
}

// AIResult JSON 往返（models.AIResult 的字段对齐）
func TestAIResultFields(t *testing.T) {
	raw := `{"index":0,"passed":true,"reason":"r","price":4500,"contact":"c","commuting":"m","confidence":0.9,"model":"m1"}`
	var ai models.AIResult
	if err := unmarshalAIResult(raw, &ai); err != nil {
		t.Fatal(err)
	}
	if ai.Price != 4500 || ai.Confidence != 0.9 || ai.Model != "m1" {
		t.Errorf("字段错误: %+v", ai)
	}
}

// 辅助：单条解析（schema.go 导出）
var _ = strings.TrimSpace
