package config

import (
	"net/url"
	"strings"
)

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

// WeiboTags 抽出高级搜索用的话题。# 话题 #、纯词、旧版搜索 URL 的 q 都能认；# 空格 当注释。
func WeiboTags(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		tag, ok := parseWeiboTagLine(line)
		if !ok || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func parseWeiboTagLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") {
		return "", false
	}
	if strings.HasPrefix(s, "#") && len(s) > 1 && (s[1] == ' ' || s[1] == '\t') {
		return "", false
	}
	if i := indexInlineHashComment(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		q := strings.TrimSpace(u.Query().Get("q"))
		if q == "" {
			return "", false
		}
		if dec, err := url.QueryUnescape(q); err == nil {
			q = dec
		}
		return normalizeWeiboTag(q)
	}
	return normalizeWeiboTag(s)
}

func normalizeWeiboTag(q string) (string, bool) {
	q = strings.TrimSpace(strings.Trim(q, "#"))
	if q == "" {
		return "", false
	}
	return "#" + q + "#", true
}

func indexInlineHashComment(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if (s[i] == ' ' || s[i] == '\t') && s[i+1] == '#' {
			return i
		}
	}
	return -1
}
