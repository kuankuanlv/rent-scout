package notifier

import (
	"fmt"
	"html"
	"strings"
)

// View 通知正文视图；先只做 html，以后可加 markdown/text
type View interface {
	Name() string
	Render(items []NotifyItem) string
}

// HTMLView 推送用的 HTML 卡片
type HTMLView struct{}

func (HTMLView) Name() string { return "html" }

func (HTMLView) Render(items []NotifyItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	group := items[0].AddressTag
	if group == "" {
		group = GroupUnknown
	}
	fmt.Fprintf(&b, "<div><p><b>%s · %d 条</b></p>", html.EscapeString(group), len(items))
	for _, it := range items {
		reason := strings.TrimSpace(it.Reason)
		if reason == "" {
			reason = "暂无"
		}
		verdict := "AI审核通过："
		if !it.Passed {
			verdict = "AI审核拒绝："
		}
		b.WriteString("<hr>")
		fmt.Fprintf(&b, "<p><b>%s</b></p>", html.EscapeString(it.Title))
		if it.URL != "" {
			fmt.Fprintf(&b, `<p><a href="%s">打开原帖</a></p>`, html.EscapeString(it.URL))
		}
		if it.Price > 0 || it.RentType != "" || it.Layout != "" {
			var info []string
			if it.Price > 0 {
				info = append(info, fmt.Sprintf("%d元/月", it.Price))
			}
			if it.RentType != "" {
				info = append(info, it.RentType)
			}
			if it.Layout != "" {
				info = append(info, it.Layout)
			}
			if it.Area != "" {
				info = append(info, it.Area)
			}
			if it.Floor != "" {
				info = append(info, it.Floor)
			}
			fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(strings.Join(info, " · ")))
		}
		if it.Contact != "" {
			fmt.Fprintf(&b, "<p>联系: %s</p>", html.EscapeString(it.Contact))
		}
		if it.Commuting != "" {
			fmt.Fprintf(&b, "<p>通勤: %s</p>", html.EscapeString(it.Commuting))
		}
		fmt.Fprintf(&b, "<p>%s%s</p>", verdict, html.EscapeString(reason))
		fmt.Fprintf(&b, `<p><a href="%s">有用</a> · <a href="%s">无用</a> · <a href="%s">标记已读</a></p>`,
			html.EscapeString(it.FeedbackURL), html.EscapeString(it.FeedbackUselessURL), html.EscapeString(it.HandledURL))
	}
	b.WriteString("</div>")
	return b.String()
}
