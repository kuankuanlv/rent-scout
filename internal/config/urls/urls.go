package urls

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

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
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

func WeiboUIDs(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		id, ok := parseWeiboUIDLine(line)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func parseWeiboUIDLine(line string) (string, bool) {
	s := trimConfigLine(line)
	if s == "" {
		return "", false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Path
	}
	s = strings.Trim(s, "/")
	if i := strings.Index(s, "/u/"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "u/")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if !isDigits(s) {
		return "", false
	}
	return s, true
}

func WeiboContainerIDs(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		id, ok := parseWeiboContainerLine(line)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func parseWeiboContainerLine(line string) (string, bool) {
	s := trimConfigLine(line)
	if s == "" {
		return "", false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Path
	}
	s = strings.Trim(s, "/")
	if i := strings.Index(s, "/p/"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "p/")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "_-_"); i > 0 {
		s = s[:i]
	}
	if len(s) < 10 {
		return "", false
	}
	return s, true
}

func trimConfigLine(line string) string {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") {
		return ""
	}
	if strings.HasPrefix(s, "#") && len(s) > 1 && (s[1] == ' ' || s[1] == '\t') {
		return ""
	}
	if i := indexInlineHashComment(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func indexInlineHashComment(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if (s[i] == ' ' || s[i] == '\t') && s[i+1] == '#' {
			return i
		}
	}
	return -1
}
