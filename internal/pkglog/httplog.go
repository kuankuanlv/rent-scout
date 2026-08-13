package pkglog

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxProbeBody = 4096

var secretHeader = map[string]bool{
	"Authorization":       true,
	"Cookie":              true,
	"Set-Cookie":          true,
	"Proxy-Authorization": true,
	"X-Api-Key":           true,
}

// ProbeHTTP 探测类请求打 raw：方法、URL、头（密钥类脱敏）、请求体、状态、应答体（截断）
func ProbeHTTP(duty, stage, method, url string, reqHeader http.Header, reqBody []byte, status int, respHeader http.Header, respBody []byte) {
	Component(duty).Info("探测 HTTP",
		"stage", stage,
		"req", formatReq(method, url, reqHeader),
		"req_body", clipBody(reqBody),
		"status", status,
		"resp_header", formatHeader(respHeader),
		"resp_body_len", len(respBody),
		"resp_body", clipBody(respBody),
	)
}

// ProbeHTTPErr 探测请求没发出去或连接失败
func ProbeHTTPErr(duty, stage, method, url string, reqHeader http.Header, err error) {
	Component(duty).Info("探测 HTTP 失败",
		"stage", stage,
		"req", formatReq(method, url, reqHeader),
		"err", err,
	)
}

func formatReq(method, url string, h http.Header) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte(' ')
	b.WriteString(url)
	if s := formatHeader(h); s != "" && s != "(无)" {
		b.WriteByte('\n')
		b.WriteString(s)
	}
	return b.String()
}

func formatHeader(h http.Header) string {
	if len(h) == 0 {
		return "(无)"
	}
	var b strings.Builder
	first := true
	for k, vals := range h {
		for _, v := range vals {
			if !first {
				b.WriteByte('\n')
			}
			first = false
			b.WriteString(k)
			b.WriteString(": ")
			if secretHeader[http.CanonicalHeaderKey(k)] {
				b.WriteString(maskHeaderValue(k, v))
			} else {
				b.WriteString(v)
			}
		}
	}
	return b.String()
}

func maskHeaderValue(key, v string) string {
	if http.CanonicalHeaderKey(key) == "Cookie" {
		return maskCookieHeader(v)
	}
	if v == "" {
		return "(空)"
	}
	return "***"
}

func maskCookieHeader(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(空)"
	}
	var parts []string
	for _, p := range strings.Split(v, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, val, ok := strings.Cut(p, "=")
		name = strings.TrimSpace(name)
		if !ok {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, name+"="+maskPreview(strings.TrimSpace(val)))
	}
	if len(parts) == 0 {
		return "(空)"
	}
	return strings.Join(parts, "; ")
}

func maskPreview(s string) string {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return ""
	}
	if n <= 8 {
		return s
	}
	r := []rune(s)
	return string(r[:4]) + "…" + string(r[len(r)-4:])
}

func clipBody(body []byte) string {
	if len(body) == 0 {
		return "(空)"
	}
	s := string(body)
	r := []rune(s)
	if len(r) > maxProbeBody {
		return string(r[:maxProbeBody]) + "\n…(截断, 共 " + itoa(len(body)) + " 字节)"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
