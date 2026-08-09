package filter

import (
	"context"
	"strings"
	"testing"

	"rent-scout/internal/models"
)

// fakeLLM 测试用：回显构造的响应（记录 system/user）
type fakeLLM struct {
	system string
	user   string
	// 按帖数构造合法响应
}

func (f *fakeLLM) Chat(ctx context.Context, system, user string) (string, error) {
	f.system = system
	f.user = user
	n := strings.Count(user, "第")
	if n == 0 {
		n = 1
	}
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, `{"index":`+itoa(i)+`,"passed":true,"reason":"ok","price":4500}`)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func itoa(i int) string {
	return string(rune('0' + i))
}

// 批量判定：system 含全部自然语言规则（共享一次）；user 含 N 条精简帖；结果按 PostID 对齐
func TestAIBatchEvaluate(t *testing.T) {
	fl := &fakeLLM{}
	ev := NewAIBatchEvaluator(fl, 500)
	posts := []models.RentPost{
		{ID: 1, Source: "douban", Title: "望京整租", Content: "近14号线，4500"},
		{ID: 2, Source: "douban", Title: "回龙观精装", Content: "两居"},
	}
	aiRules := []models.Rule{
		{ID: 10, Type: models.RuleTypeAINatural, Value: "只要地铁1公里内的整租", Enabled: true},
	}
	results, err := ev.EvaluateBatch(context.Background(), posts, aiRules)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("结果数 = %d, want 2", len(results))
	}
	if r := results[1]; r == nil || !r.Passed || r.Price != 4500 {
		t.Errorf("post 2 结果错误: %+v", r)
	}
	// system 含规则文本（共享一次）；user 含两条帖子且去 HTML
	if !strings.Contains(fl.system, "地铁1公里") {
		t.Error("system 应含自然语言规则")
	}
	if strings.Contains(fl.user, "<p>") || !strings.Contains(fl.user, "望京整租") {
		t.Error("user 应为精简帖（无 HTML，含标题）")
	}
}

// LLM 失败：整批返回错误（调用方保持待判定，不误标记——规格 5.6）
func TestAIBatchLLMFailure(t *testing.T) {
	fl := &fakeLLM{}
	ev := NewAIBatchEvaluator(fl, 500)
	// 构造解析失败：fakeLLM 的 Chat 返回非法内容——用一个失败注入
	fl2 := &failLLM{}
	ev2 := NewAIBatchEvaluator(fl2, 500)
	posts := []models.RentPost{{ID: 1, Source: "douban", Title: "t", Content: "c"}}
	if _, err := ev2.EvaluateBatch(context.Background(), posts, []models.Rule{{Type: models.RuleTypeAINatural}}); err == nil {
		t.Fatal("LLM 失败应整批报错")
	}
	_ = ev
}

type failLLM struct{}

func (f *failLLM) Chat(ctx context.Context, system, user string) (string, error) {
	return "", context.DeadlineExceeded
}
