package filter

import (
	"context"
	"testing"
	"time"

	"rent-scout/internal/models"
)

// 硬编码链：白名单短路定案（decided=true），产出 tags 与命中详情
func TestRuleChainWhitelistDecided(t *testing.T) {
	chain := NewRuleChain(nil)
	post := models.RentPost{ID: 1, Title: "望京整租", Content: "近14号线", CollectedAt: time.Now()}
	rules := []models.Rule{{ID: 1, Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "望京", Priority: 10}}
	res, tags, decided, err := chain.EvaluateHard(context.Background(), post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !decided || res.Status != models.PostStatusPassed || res.Stage != models.StageHardRule {
		t.Errorf("白名单应定案 passed: decided=%v %+v", decided, res)
	}
	if len(tags) != 1 || tags[0] != "望京" {
		t.Errorf("tags = %v, want [望京]（Consumer 负责写库）", tags)
	}
	if res.HardRules == nil || len(res.HardRules) != 1 {
		t.Errorf("命中详情缺失: %+v", res.HardRules)
	}
}

// 硬编码链：未命中任何规则 → 未定案（decided=false），交由 AI 批/默认放行
func TestRuleChainUndecided(t *testing.T) {
	chain := NewRuleChain(nil)
	post := models.RentPost{ID: 2, Title: "普通帖子", Content: "x", CollectedAt: time.Now()}
	rules := []models.Rule{{ID: 1, Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介", Priority: 10}}
	res, _, decided, err := chain.EvaluateHard(context.Background(), post, rules)
	if err != nil {
		t.Fatal(err)
	}
	if decided {
		t.Errorf("黑名单未命中不应定案: %+v", res)
	}
}

// fakeAI 测试用：实现 AIEvaluator 接口（批量判定，全部通过）
type fakeAI struct{}

func (f *fakeAI) EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error) {
	results := map[int64]*models.AIResult{}
	for _, p := range posts {
		results[p.ID] = &models.AIResult{Passed: true, Reason: "ok", Price: 4500, Confidence: 0.9}
	}
	return results, nil
}

// AI 批量：未定案帖子一次调用；结果含 AI 详情与拒绝原因
func TestRuleChainAIBatch(t *testing.T) {
	chain := NewRuleChain(&fakeAI{})
	if !chain.HasAI() {
		t.Fatal("HasAI 应为 true")
	}
	posts := []models.RentPost{
		{ID: 1, Source: "douban", Title: "望京整租", Content: "近14号线，4500", CollectedAt: time.Now()},
		{ID: 2, Source: "douban", Title: "回龙观", Content: "两居", CollectedAt: time.Now()},
	}
	rules := []models.Rule{{ID: 10, Type: models.RuleTypeAINatural, Value: "只要地铁1公里内", Enabled: true}}
	results, err := chain.EvaluateAIBatch(context.Background(), posts, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("结果数 = %d, want 2", len(results))
	}
	if r := results[1]; r.Status != models.PostStatusPassed || r.Stage != models.StageAIRule || r.AI == nil {
		t.Errorf("post 2 结果错误: %+v", r)
	}
}

// 无 AI 规则时：EvaluateAIBatch 报错（Consumer 走默认放行路径）
func TestRuleChainAIBatchNoRules(t *testing.T) {
	chain := NewRuleChain(&fakeAI{})
	if _, err := chain.EvaluateAIBatch(context.Background(),
		[]models.RentPost{{ID: 1}}, []models.Rule{{ID: 1, Type: models.RuleTypeHardBlacklist}}); err == nil {
		t.Fatal("无自然语言规则应报错")
	}
}
