package filter

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/filter/llm"
	"rent-scout/internal/models"
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
func NewAIBatchEvaluator(c llmChat, _ map[string]int) *AIBatchEvaluator {
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
	raw, model, err := e.chat(ctx, buildSystemPrompt(aiRules), sb.String())
	if err != nil {
		return nil, fmt.Errorf("AI 批量判定请求失败: %w", err)
	}
	parsed, err := llm.ParseAIResults(raw, len(posts))
	if err != nil {
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

// buildSystemPrompt 固定 system prompt（规则集共享一次，调整规格 C）：
// 规则 + 判定标准 + 输出 Schema（简洁指令，无示例膨胀）
func buildSystemPrompt(aiRules []models.Rule) string {
	var rules []string
	for _, r := range aiRules {
		rules = append(rules, fmt.Sprintf("- %s（规则#%d）", r.Value, r.ID))
	}
	return fmt.Sprintf(`你是租房信息筛选助手。根据用户设定的筛选规则判断每套房源帖子是否合格，并抽取关键字段。

筛选规则：
%s

判定标准：
- 帖子满足任一规则的要求即通过（passed=true），否则拒绝
- 只依据帖子内容与规则判定，不做无依据推测
- 价格：识别月租金（元），无法确定填 0
- 联系：提取联系人称呼或联系方式，没有填空串
- 通勤：提取帖子中提到的通勤/交通描述，没有填空串

输出 JSON 数组（与输入帖子顺序一一对应），每项格式：
{"index":序号,"passed":true或false,"reason":"通过或拒绝的简短理由（中文30字内）","price":整数,"contact":"字符串","commuting":"字符串","confidence":0到1浮点数}
只输出 JSON，不要输出其他内容。`, strings.Join(rules, "\n"))
}
