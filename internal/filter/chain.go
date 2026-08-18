package filter

import (
	"context"
	"fmt"
	"time"

	"rent-scout/internal/models"
)

// AIEvaluator AI 规则链评估接口（批量）：llm 客户端注入，便于测试替换
type AIEvaluator interface {
	// EvaluateBatch 批量判定；返回每帖结果（index 与输入对齐）。
	// 返回 error = 整批瞬时失败（调用方不写 ai_result，下轮再捞）
	EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error)
}

// RuleChain 规则链执行器（规格 5.3）：硬编码链 → AI 链。
// 编排：filter 协程逐帖 EvaluateHard；未定案交 ai_review 从库凑批 EvaluateAIBatch
type RuleChain struct {
	ai AIEvaluator // nil = AI 未启用
}

// NewRuleChain 创建规则链；ai 为 nil 时 AI 协程空捞，硬规则仍独立定案
func NewRuleChain(ai AIEvaluator) *RuleChain {
	return &RuleChain{ai: ai}
}

// HasAI 是否注入了 LLM 客户端（仅 AI 协程用来决定能不能审）
func (c *RuleChain) HasAI() bool {
	return c.ai != nil
}

// EvaluateHard 硬编码链：只看白名单地点，总会定案。
// 返回结果 + 白名单地点（Consumer 写 post_tags location）
func (c *RuleChain) EvaluateHard(ctx context.Context, post models.RentPost, rules []models.Rule) (models.FilterResult, []string, error) {
	now := time.Now()
	v, tags, hits, rejectedBy, err := EvaluateHard(post, rules)
	if err != nil {
		return models.FilterResult{}, nil, err
	}
	status := models.PostStatusPassed
	if !v.Passed {
		status = models.PostStatusRejected
	}
	return models.FilterResult{
		PostID: post.ID, Status: status, Stage: models.StageHardRule,
		RejectedBy: rejectedBy, DecidedAt: now, HardRules: hits,
	}, tags, nil
}

// EvaluateAIBatch 批量 AI 判定（规格 5.4 + 调整 C）：未定案的帖子一次 LLM 调用。
// 返回 map[PostID]FilterResult（含 AI 详情）。任何失败整批报错（Consumer 保持 pending 下轮重试）
func (c *RuleChain) EvaluateAIBatch(ctx context.Context, posts []models.RentPost, rules []models.Rule) (map[int64]models.FilterResult, error) {
	aiRules := enabledAIRules(rules)
	if c.ai == nil || len(aiRules) == 0 {
		return nil, fmt.Errorf("AI 链不可用")
	}
	aiResults, err := c.ai.EvaluateBatch(ctx, posts, aiRules)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	results := make(map[int64]models.FilterResult, len(posts))
	for _, post := range posts {
		ai := aiResults[post.ID]
		if ai == nil {
			return nil, fmt.Errorf("AI 输出缺失 post %d", post.ID)
		}
		results[post.ID] = models.FilterResult{
			PostID: post.ID, Status: models.PostStatusPassed, Stage: models.StageAIRule,
			DecidedAt: now, AI: ai,
		}
	}
	return results, nil
}

func enabledAIRules(rules []models.Rule) []models.Rule {
	var out []models.Rule
	for _, r := range rules {
		if r.Type == models.RuleTypeAINatural && r.Enabled {
			out = append(out, r)
		}
	}
	return out
}
