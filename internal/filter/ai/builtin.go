package ai

import "strings"

// BuiltInPrompt 精简内置固定 Prompt（hover 展示完整版）
const BuiltInPrompt = `你是租房帖审核员，仅输出 JSON{passed,reason}。房东直租/转租优先，信息完整、价格合理、非求租/非中介包装。白名单命中即通过，其余由你综合判定。不确定宁可拒绝，理由限中文约30字。`

func trimExpectation(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		r = r[:120]
	}
	return string(r)
}

// BuildSystemPromptWithExpectation 在原 system prompt 后追加用户期许
func BuildSystemPromptWithExpectation(base string, expectation string) string {
	if exp := trimExpectation(expectation); exp != "" {
		return base + "\n\n用户期许：" + exp
	}
	return base
}
