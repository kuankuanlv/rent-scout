package filter

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/filter/llm"
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

// NewAIBatchEvaluator 创建批量评估器；截断统一用 DefaultTrimLimit（不再读配置 map）
func NewAIBatchEvaluator(c llmChat) *AIBatchEvaluator {
	return &AIBatchEvaluator{llm: c}
}

// EvaluateBatch 批量判定：返回 map[PostID]*AIResult（index 与输入对齐）。
// 任何失败（请求/解析/数量不匹配）→ 整批报错（调用方保持待判定下轮重试，规格 5.6）
func (e *AIBatchEvaluator) EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error) {
	if len(posts) == 0 {
		return map[int64]*models.AIResult{}, nil
	}
	// 构造精简帖（Trim：去 HTML/图片，统一 DefaultTrimLimit）
	var sb strings.Builder
	for i, p := range posts {
		v := BuildLLMView(p, DefaultTrimLimit)
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
	system := buildSystemPrompt(aiRules)
	user := sb.String()
	raw, model, err := e.chat(ctx, system, user)
	logAITurn(len(posts), model, system, user, raw, err)
	if err != nil {
		return nil, fmt.Errorf("AI 批量判定请求失败: %w", err)
	}
	parsed, err := llm.ParseAIResults(raw, len(posts))
	if err != nil {
		pkglog.Component(pkglog.AIReview).Error("LLM 返回解析失败", "posts", len(posts), "reply", raw, "err", err)
		return nil, fmt.Errorf("AI 批量判定解析失败: %w", err)
	}
	// index → PostID 对齐；回填实际命中的模型名（规格 3.2 Model = 实际使用的模型）
	results := make(map[int64]*models.AIResult, len(posts))
	for i, ai := range parsed {
		ai.Model = model
		results[posts[i].ID] = &ai
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
func buildSystemPrompt(aiRules []models.Rule) string {
	_ = aiRules
	return fmt.Sprintf(`你是租房信息筛选助手。按内置标准判断每套房源是否合格，并抽取关键字段。
帖子若含月租、联系方式，一并在 JSON 中返回。数组长度必须与输入帖子数量相同。

内置筛选标准：
%s

抽取约定：
- 只依据帖子内容判定，不做无依据推测
- 价格：月租金整数（元）。月租3000填3000，房租2500/月填2500，2000-2500填2000。无法确定填 0
- 联系：提取微信/手机号等原文，没有填空串
- 通勤：提取帖子中提到的通勤/交通描述，没有填空串

只输出纯 JSON，不要 markdown 或解释。优先输出 {"verdicts":[...]}，每项：
{"index":序号从0起,"passed":true或false,"reason":"中文30字内","price":整数,"contact":"字符串","commuting":"字符串","confidence":0到1}`, models.BuiltInAIRuleValue)
}
