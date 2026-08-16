package collector

import "strings"

// PlainText 去掉多余空白，不保留图片。
func PlainText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
