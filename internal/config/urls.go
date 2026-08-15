package config

import "strings"

// HTTPURLs 从多行源地址里抽出 http(s)。整行注释（# 开头或不是网址）忽略；网址后面空格加井号当行内注释。
func HTTPURLs(lines []string) []string {
	var out []string
	for _, line := range lines {
		if u, ok := parseSourceURLLine(line); ok {
			out = append(out, u)
		}
	}
	return out
}

// FirstHTTPURL 多行文本里第一条网址，没有则空
func FirstHTTPURL(raw string) string {
	urls := HTTPURLs(splitLines(raw))
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

func parseSourceURLLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	if i := indexInlineHashComment(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s, true
	}
	return "", false
}

func indexInlineHashComment(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if (s[i] == ' ' || s[i] == '\t') && s[i+1] == '#' {
			return i
		}
	}
	return -1
}
