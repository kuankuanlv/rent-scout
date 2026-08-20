package ai

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/filter/ai/llm"
	"rent-scout/internal/filter/rule"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

// llmChat LLM 对话接口（llm.Client/Pool 均满足；测试注入 fake）
type llmChat interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

// AIBatchEvaluator AI 批量评估器（规格 5.4 + 调整 C）：
// system 固定（规则集+判定标准+Schema，全批共享一次） + user 只放 N 条精简帖
type AIBatchEvaluator struct {
	llm llmChat
}

// NewAIBatchEvaluator 创建批量评估器；截断统一用 rule.DefaultTrimLimit（不再读配置 map）
func NewAIBatchEvaluator(c llmChat) *AIBatchEvaluator {
	return &AIBatchEvaluator{llm: c}
}

// EvaluateBatch 批量判定：返回 map[PostID]*AIResult（index 与输入对齐）。
// 请求失败仍整批报错；JSON 被截断时先收下完整项，再对缺口补跑一轮。
func (e *AIBatchEvaluator) EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error) {
	if len(posts) == 0 {
		return map[int64]*models.AIResult{}, nil
	}
	// 构造精简帖（Trim：去 HTML/图片，统一 rule.DefaultTrimLimit）
	var sb strings.Builder
	for i, p := range posts {
		v := rule.BuildLLMView(p, rule.DefaultTrimLimit)
		sb.WriteString(fmt.Sprintf("第%d条 [%s] 标题：%s\n", i, v.Source, v.Title))
		if v.URL != "" {
			sb.WriteString("链接：" + v.URL + "\n")
		}
		if v.Content != "" {
			sb.WriteString("内容：" + v.Content + "\n")
		}
		if i < len(posts)-1 {
			sb.WriteString("---\n")
		}
	}
	system := buildSystemPrompt(aiRules, len(posts))
	user := fmt.Sprintf("本批共 %d 条帖子，verdicts 必须恰好 %d 项（index 为 0 到 %d），一帖一条综合判定，禁止把同一帖拆成多条。\n\n%s",
		len(posts), len(posts), len(posts)-1, sb.String())
	raw, model, err := e.chat(ctx, system, user)
	logAITurn(len(posts), model, system, user, raw, err)
	if err != nil {
		return nil, fmt.Errorf("AI 批量判定请求失败: %w", err)
	}
	parsed, filled, err := llm.ParseAIResultsPartial(raw, len(posts))
	if err != nil {
		pkglog.Component(pkglog.AIReview).Error("LLM 返回解析失败", "posts", len(posts), "reply", raw, "err", err)
		return nil, fmt.Errorf("AI 批量判定解析失败: %w", err)
	}
	results := make(map[int64]*models.AIResult, len(posts))
	var missing []models.RentPost
	for i, p := range posts {
		if parsed[i].RawResponse == "" {
			missing = append(missing, p)
			continue
		}
		ai := parsed[i]
		ai.Model = model
		results[p.ID] = &ai
	}
	if len(missing) == 0 {
		return results, nil
	}
	pkglog.Component(pkglog.AIReview).Warn("LLM 输出截断，补跑未齐的帖", "got", filled, "retry", len(missing), "posts", len(posts))
	rest, err := e.EvaluateBatch(ctx, missing, aiRules)
	if err != nil {
		return nil, err
	}
	for id, ai := range rest {
		results[id] = ai
	}
	return results, nil
}

// llmChatWithModel 可选接口：透传实际命中的模型名（Pool 实现；普通 client/fake 不实现）。
// 通过类型断言探测 + 兜底，不破坏既有 llmChat 接口（测试 fake 无需改动）
type llmChatWithModel interface {
	ChatWithModel(ctx context.Context, system, user string) (string, string, error)
}

// chat 调用 LLM：优先探测 ChatWithModel（回填实际模型名），否则退化 Chat（Model 留空）
func (e *AIBatchEvaluator) chat(ctx context.Context, system, user string) (string, string, error) {
	if cwm, ok := e.llm.(llmChatWithModel); ok {
		return cwm.ChatWithModel(ctx, system, user)
	}
	raw, err := e.llm.Chat(ctx, system, user)
	return raw, "", err
}

func logAITurn(n int, model, system, user, reply string, err error) {
	log := pkglog.Component(pkglog.AIReview)
	args := []any{"posts", n, "model", model, "system", system, "user", user, "reply", reply}
	if err != nil {
		log.Error("LLM 一轮对话失败", append(args, "err", err)...)
		return
	}
	log.Info("LLM 一轮对话", args...)
}

// buildSystemPrompt 固定 system prompt（规则集共享一次，调整规格 C）：
// 规则 + 判定标准 + 输出 Schema（简洁指令，无示例膨胀）
func buildSystemPrompt(aiRules []models.Rule, n int) string {
	_ = aiRules
	return fmt.Sprintf(`租房筛选助手。判断房源是否靠谱（非中介/骗子/不实），并抽取信息字段。
本批 %d 条，verdicts 长度必须 = %d，每帖一条。

筛选标准：
%s

字段抽取（尽力而为，缺失填零值，不影响 passed）：
- price: 月租整数（元），区间取低值，不确定填 0
- contact: 微信/手机号原文，没有填""
- commuting: 通勤/交通描述，没有填""
- layout: 户型如"两室一厅""次卧"，没有填""
- rentType: "整租"或"合租"，不确定填""
- floor: 楼层如"6楼""高层"，没有填""
- area: 面积如"60平""15㎡"，没有填""

只输出纯 JSON：{"verdicts":[...]}，每项：
{"index":0起,"passed":bool,"reason":"中文20字内","price":int,"contact":"","commuting":"","layout":"","rentType":"","floor":"","area":"","confidence":0~1}`, n, n, models.BuiltInAIRuleValue)
}
