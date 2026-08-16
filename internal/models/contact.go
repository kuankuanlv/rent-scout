package models

import (
	"regexp"
	"strings"
	"unicode"
)

const ContactUnknown = "暂无"

var (
	contactWeChatRe      = regexp.MustCompile(`(?i)(?:微信号?|薇信|威信|v信|ｖ信|加微信|加个微信|加v\b|加V\b|wechat|weixin|\bwx\b|\bvx\b)\s*[:：是为]?\s*([a-zA-Z][-_a-zA-Z0-9]{5,19})`)
	contactWeChatDigitRe = regexp.MustCompile(`(?i)(?:微信号?|薇信|威信|v信|加微信|wechat|weixin|\bwx\b|\bvx\b)\s*[:：是为]?\s*([1-9][-_a-zA-Z0-9]{5,19})`)
	contactPhoneRe       = regexp.MustCompile(`(?:\+?86[-\s]?)?(1[3-9]\d{9})`)
	contactPhoneLooseRe  = regexp.MustCompile(`(?:\+?86[-\s.]?)?(1[3-9](?:[\s.\-·•＊*_－—]?\d){9})`)
	contactPhoneLabelRe  = regexp.MustCompile(`(?i)(?:电话|手机号?|联系电话|电联|拨打|call|tel)\s*[:：是为]?\s*(?:\+?86[-\s]*)?(1[3-9](?:[\s.\-·•＊*_－—]?\d){9})`)
	contactEmailRe       = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	contactQQRe          = regexp.MustCompile(`(?i)(?:qq|扣扣|企鹅号?)\s*[:：是为]?\s*([1-9]\d{4,11})`)
)

// ExtractContact 尽力抠微信/手机/邮箱/QQ，只读标题正文，抠不到就暂无
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
	for _, m := range contactWeChatDigitRe.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			id := m[1]
			if d := digitsOnly(id); isCNMobile(d) {
				add(d)
				continue
			}
			add(id)
		}
	}

	fw := mapFullwidthDigits(text)
	for _, src := range []string{text, fw} {
		for _, m := range contactPhoneLabelRe.FindAllStringSubmatch(src, -1) {
			if len(m) >= 2 {
				addPhone(add, m[1])
			}
		}
		for _, m := range contactPhoneRe.FindAllStringSubmatch(src, -1) {
			if len(m) >= 2 {
				addPhone(add, m[1])
			}
		}
		for _, m := range contactPhoneLooseRe.FindAllStringSubmatch(src, -1) {
			if len(m) >= 2 {
				addPhone(add, m[1])
			}
		}
	}
	for _, p := range phonesFromChineseDigits(fw) {
		addPhone(add, p)
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

func addPhone(add func(string), raw string) {
	d := digitsOnly(raw)
	if isCNMobile(d) {
		add(d)
	}
}

func isCNMobile(s string) bool {
	if len(s) != 11 || s[0] != '1' || s[1] < '3' || s[1] > '9' {
		return false
	}
	for i := 0; i < 11; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
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

func mapFullwidthDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteByte(byte('0' + (r - '０')))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var cnPhoneDigit = map[rune]rune{
	'零': '0', '〇': '0', '○': '0', 'Ｏ': '0', 'O': '0', 'o': '0',
	'一': '1', '壹': '1', '幺': '1', '①': '1',
	'二': '2', '贰': '2', '②': '2',
	'三': '3', '叁': '3', '③': '3',
	'四': '4', '肆': '4', '④': '4',
	'五': '5', '伍': '5', '⑤': '5',
	'六': '6', '陆': '6', '⑥': '6',
	'七': '7', '柒': '7', '⑦': '7',
	'八': '8', '捌': '8', '⑧': '8',
	'九': '9', '玖': '9', '⑨': '9',
}

func phonesFromChineseDigits(s string) []string {
	var buf []byte
	flush := func(out *[]string) {
		if isCNMobile(string(buf)) {
			*out = append(*out, string(buf))
		}
		buf = buf[:0]
	}
	var out []string
	for _, r := range s {
		if r >= '0' && r <= '9' {
			buf = append(buf, byte(r))
			continue
		}
		if d, ok := cnPhoneDigit[r]; ok {
			buf = append(buf, byte(d))
			continue
		}
		if unicode.IsSpace(r) || strings.ContainsRune("-.·•＊*_－—", r) {
			continue
		}
		flush(&out)
	}
	flush(&out)
	return out
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

// FillPostContact 入库前补联系方式：调用方没填才正则抽，不改标题正文
func FillPostContact(p *RentPost) {
	if p == nil {
		return
	}
	if HasContact(p.Contact) {
		return
	}
	p.Contact = ExtractContact(p.Title, p.Content)
}
