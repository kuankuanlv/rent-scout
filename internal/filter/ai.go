package filter

import (
	"context"
	"fmt"
	"strings"

	"rent-scout/internal/models"
	"rent-scout/internal/filter/llm"
)

// llmChat LLM 对话接口（llm.Client/Pool 均满足；测试注入 fake）
type llmChat interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

// AIBatchEvaluator AI 批量评估器（规格 5.4 + 调整 C）：
// system 固定（规则集+判定标准+Schema，全批共享一次） + user 只放 N 条精简帖
type AIBatchEvaluator struct {
	llm     llmChat
	trimLen int // 每帖截断字数（trim_limits[source]，缺省 500）
}

// NewAIBatchEvaluator 创建批量评估器
func NewAIBatchEvaluator(c llmChat, trimLen int) *AIBatchEvaluator {
	return &AIBatchEvaluator{llm: c, trimLen: trimLen}
}

// EvaluateBatch 批量判定：返回 map[PostID]*AIResult（index 与输入对齐）。
// 任何失败（请求/解析/数量不匹配）→ 整批报错（调用方保持待判定下轮重试，规格 5.6）
func (e *AIBatchEvaluator) EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error) {
	if len(posts) == 0 {
		return map[int64]*models.AIResult{}, nil
	}
	// 构造精简帖（Trim：去 HTML/图片，按源截断——调整规格 C 省 token）
	var sb strings.Builder
	for i, p := range posts {
		v := BuildLLMView(p, e.trimLen)
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
	raw, err := e.llm.Chat(ctx, buildSystemPrompt(aiRules), sb.String())
	if err != nil {
		return nil, fmt.Errorf("AI 批量判定请求失败: %w", err)
	}
	parsed, err := llm.ParseAIResults(raw, len(posts))
	if err != nil {
		return nil, fmt.Errorf("AI 批量判定解析失败: %w", err)
	}
	// index → PostID 对齐
	results := make(map[int64]*models.AIResult, len(posts))
	for i, ai := range parsed {
		results[posts[i].ID] = &ai
	}
	return results, nil
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
