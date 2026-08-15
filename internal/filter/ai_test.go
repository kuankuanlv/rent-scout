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
	ev := NewAIBatchEvaluator(fl)
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
	ev2 := NewAIBatchEvaluator(&failLLM{})
	posts := []models.RentPost{{ID: 1, Source: "douban", Title: "t", Content: "c"}}
	if _, err := ev2.EvaluateBatch(context.Background(), posts, []models.Rule{{Type: models.RuleTypeAINatural}}); err == nil {
		t.Fatal("LLM 失败应整批报错")
	}
}

type failLLM struct{}

func (f *failLLM) Chat(ctx context.Context, system, user string) (string, error) {
	return "", context.DeadlineExceeded
}

// Plan 10 E1：截断统一 DefaultTrimLimit，不再按源读配置 map
func TestAIBatchEvaluatorDefaultTrim(t *testing.T) {
	fl := &fakeLLM{}
	ev := NewAIBatchEvaluator(fl) // 截断统一 DefaultTrimLimit
	long := strings.Repeat("甲", DefaultTrimLimit+50)
	posts := []models.RentPost{
		{ID: 1, Source: "douban", Title: "望京", Content: long},
		{ID: 2, Source: "beike", Title: "回龙观", Content: long},
	}
	aiRules := []models.Rule{{ID: 10, Type: models.RuleTypeAINatural, Value: "只要整租", Enabled: true}}
	if _, err := ev.EvaluateBatch(context.Background(), posts, aiRules); err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("甲", DefaultTrimLimit)
	if !strings.Contains(fl.user, want) || strings.Contains(fl.user, strings.Repeat("甲", DefaultTrimLimit+1)) {
		t.Error("各源均应按 DefaultTrimLimit 截断")
	}
}

// fakeLLMWithModel 测试用：实现可选接口 ChatWithModel（模拟 Pool 透传实际模型名）
type fakeLLMWithModel struct {
	fakeLLM
	model string
}

func (f *fakeLLMWithModel) ChatWithModel(ctx context.Context, system, user string) (string, string, error) {
	out, err := f.fakeLLM.Chat(ctx, system, user)
	return out, f.model, err
}

// I3（最终审查）：AIResult.Model 回填实际命中的模型名（规格 3.2 Model = 实际使用的模型）。
// 探测 ChatWithModel 接口 → 回填；未实现（普通 client/fake）→ 兜底留空，不破坏 llmChat
func TestAIBatchEvaluatorModelBackfill(t *testing.T) {
	t.Run("ChatWithModel 实现者回填模型名", func(t *testing.T) {
		fl := &fakeLLMWithModel{model: "deepseek-chat"}
		ev := NewAIBatchEvaluator(fl)
		posts := []models.RentPost{{ID: 1, Source: "douban", Title: "望京", Content: "近地铁"}}
		results, err := ev.EvaluateBatch(context.Background(), posts, []models.Rule{{Type: models.RuleTypeAINatural, Enabled: true}})
		if err != nil {
			t.Fatal(err)
		}
		if got := results[1].Model; got != "deepseek-chat" {
			t.Errorf("Model = %q, want deepseek-chat", got)
		}
	})
	t.Run("仅实现 Chat 的 fake 兜底 Model 留空", func(t *testing.T) {
		fl := &fakeLLM{}
		ev := NewAIBatchEvaluator(fl)
		posts := []models.RentPost{{ID: 1, Source: "douban", Title: "望京", Content: "近地铁"}}
		results, err := ev.EvaluateBatch(context.Background(), posts, []models.Rule{{Type: models.RuleTypeAINatural, Enabled: true}})
		if err != nil {
			t.Fatal(err)
		}
		if got := results[1].Model; got != "" {
			t.Errorf("Model = %q, want 空（未实现 ChatWithModel）", got)
		}
	})
}
