package rule

import (
	"strings"

	"rent-scout/internal/models"
)

// DefaultTrimLimit 正文截断默认上限（rune）。BuildLLMView 在 limit<=0 时内部回退到此值。
const DefaultTrimLimit = 500

// LLMView LLM 输入精简视图（规格 5.2 + 调整 C）：标题 + 关键字段 + 正文截断。
// 去 HTML 标签/图片链接省 token；正文按 limit（rune）截断，避免切碎中文字符。
type LLMView struct {
	Source  string // 源标识（douban / beike / ...）
	Title   string
	URL     string // 原文链接
	Content string // 去 HTML 后的纯文本正文（已按 limit 截断）
}

// BuildLLMView 构建 LLM 输入精简视图。
// limit<=0 时内部按 DefaultTrimLimit（500）截断（非调用方职责）。
// 截断按 rune 计数，中文字符不会被切碎。
func BuildLLMView(post models.RentPost, limit int) LLMView {
	if limit <= 0 {
		limit = DefaultTrimLimit
	}
	return LLMView{
		Source:  post.Source,
		Title:   post.Title,
		URL:     post.URL,
		Content: truncateRunes(stripPageJunk(stripHTML(post.Content)), limit),
	}
}

// truncateRunes 按 rune 截断字符串（多字节安全）；超长才截断，末尾不加省略号（LLM 输入不需要）
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// stripHTML 去 HTML 标签/图片链接并压缩连续空白（状态机逐字符）。
// Content 来自详情页 HTML：跳过 <tag>（含 <img src=...>，图片链接不进正文），
// 标签间空白（缩进/换行残留）压缩为单个空格，保留文本内容。
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	spacePending := false // 刚写入过空白：连续空白只保留一个
	flushSpace := func() {
		if !spacePending {
			b.WriteByte(' ')
			spacePending = true
		}
	}
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case inTag:
			// 标签内部（含属性值、img src URL）一律跳过
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flushSpace()
		default:
			b.WriteRune(r)
			spacePending = false
		}
	}
	return strings.TrimSpace(b.String())
}

// stripPageJunk 豆瓣详情常把页面脚本拼进正文，截断前先砍掉，少占 token、也少干扰模型
func stripPageJunk(s string) string {
	for _, mark := range []string{
		"$(function",
		"var _topicOptConfig",
		"window.createGroupReportButton",
	} {
		if i := strings.Index(s, mark); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	return s
}
