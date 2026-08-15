package filter

import (
	"strings"
	"testing"
	"unicode/utf8"

	"rent-scout/internal/models"
)

// 默认截断：limit<=0 时 BuildLLMView 内部按默认 500（rune）截断（规格 5.2 + 调整 C）
func TestBuildLLMViewDefaultLimit(t *testing.T) {
	long := strings.Repeat("好", 1200)
	post := models.RentPost{Source: "douban", Title: "望京整租", URL: "https://x", Content: long}
	for _, limit := range []int{0, -1} {
		v := BuildLLMView(post, limit)
		n := utf8.RuneCountInString(v.Content)
		if n > DefaultTrimLimit {
			t.Errorf("limit=%d: 截断后 %d runes, want ≤ %d", limit, n, DefaultTrimLimit)
		}
		if n == 0 {
			t.Errorf("limit=%d: 正文不应为空", limit)
		}
		if v.Source != post.Source || v.Title != post.Title || v.URL != post.URL {
			t.Errorf("limit=%d: 关键字段丢失: %+v", limit, v)
		}
	}
}

// stripHTML 状态机：去标签/图片链接（img src 不进入正文）+ 压缩连续空白；
// BuildLLMView 按 rune 截断（多字节安全，不切碎中文字符）
func TestBuildLLMViewStripsTruncates(t *testing.T) {
	html := "<div class=\"topic-content\">\n  <p>近地铁14号线，<b>整租</b> 4500</p>\n" +
		"<img src=\"https://img.douban.com/pic/x.jpg\">\n  <br>\n  <p>联系人：张先生 电话 13800000000</p>\n</div>"
	post := models.RentPost{Source: "douban", Title: "望京整租", URL: "https://www.douban.com/group/topic/1/", Content: html}

	// stripHTML 单测：标签/图片链接清除 + 空白压缩
	plain := stripHTML(html)
	if strings.Contains(plain, "<") || strings.Contains(plain, ">") {
		t.Errorf("HTML 标签残留: %q", plain)
	}
	if strings.Contains(plain, "img.douban.com") || strings.Contains(plain, "src=") {
		t.Errorf("图片链接进入正文: %q", plain)
	}
	if strings.Contains(plain, "\n") || strings.Contains(plain, "\t") || strings.Contains(plain, "  ") {
		t.Errorf("连续空白未压缩: %q", plain)
	}
	if !strings.Contains(plain, "近地铁14号线") || !strings.Contains(plain, "联系人：张先生") {
		t.Errorf("正文文本丢失: %q", plain)
	}

	// BuildLLMView 按 rune 截断：limit=6，中文字符不得被切碎
	v := BuildLLMView(models.RentPost{Content: "一二三四五六七八九十"}, 6)
	if v.Content != "一二三四五六" {
		t.Errorf("rune 截断错误: %q", v.Content)
	}
	if n := utf8.RuneCountInString(v.Content); n != 6 {
		t.Errorf("截断后 %d runes, want 6", n)
	}

	// 完整视图：正文截断到 limit，关键字段保留
	v2 := BuildLLMView(post, 8)
	if n := utf8.RuneCountInString(v2.Content); n > 8 {
		t.Errorf("正文超过 limit: %d", n)
	}
	if v2.Title != post.Title || v2.URL != post.URL || v2.Source != post.Source {
		t.Errorf("关键字段丢失: %+v", v2)
	}
}
