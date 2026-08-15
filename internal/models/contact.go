package models

import (
	"regexp"
	"strings"
	"unicode"
)

const ContactUnknown = "暂无"

var (
	contactWeChatRe = regexp.MustCompile(`(?i)(?:微信号?|v信|加微信|加个微信|wechat|weixin|\bwx\b|\bvx\b)\s*[:：是为]?\s*([a-zA-Z][-_a-zA-Z0-9]{5,19})`)
	contactPhoneRe  = regexp.MustCompile(`(?:\+?86[-\s]?)?(1[3-9]\d{9})`)
	contactPhoneLooseRe = regexp.MustCompile(`(?:\+?86[-\s]?)?(1[3-9]\d{1}[-\s]?\d{4}[-\s]?\d{4})`)
	contactEmailRe  = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	contactQQRe     = regexp.MustCompile(`(?i)(?:qq|扣扣)\s*[:：是为]?\s*([1-9]\d{4,11})`)
)

// ExtractContact 从标题正文尽量抠微信/手机/邮箱/QQ，没有就暂无
func ExtractContact(title, content string) string {
	text := title + "\n" + content
	var parts []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		parts = append(parts, s)
	}
	for _, m := range contactWeChatRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	for _, m := range contactPhoneRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	if len(parts) == 0 {
		for _, m := range contactPhoneLooseRe.FindAllStringSubmatch(text, -1) {
			if len(m) >= 2 {
				add(digitsOnly(m[1]))
			}
		}
	}
	for _, m := range contactEmailRe.FindAllString(text, -1) {
		add(m)
	}
	for _, m := range contactQQRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("QQ:" + m[1])
		}
	}
	if len(parts) == 0 {
		return ContactUnknown
	}
	return strings.Join(parts, " / ")
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HasContact 库里的联系方式是否算抽到了
func HasContact(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == ContactUnknown {
		return false
	}
	switch strings.ToLower(s) {
	case "无", "没有", "未知", "none", "n/a", "-", "空":
		return false
	}
	return true
}

// FillPostContact 入库前补联系方式：调用方没填才正则抽
func FillPostContact(p *RentPost) {
	if p == nil {
		return
	}
	if HasContact(p.Contact) {
		return
	}
	p.Contact = ExtractContact(p.Title, p.Content)
}
