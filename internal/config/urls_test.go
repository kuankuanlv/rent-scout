package config

import "testing"

func TestHTTPURLsSkipsComments(t *testing.T) {
	lines := []string{
		"#北京租房",
		"https://s.weibo.com/weibo?q=%23a%23",
		"租房",
		"https://s.weibo.com/weibo?q=%23b%23 # 行内备注",
		"",
		"# 整行注释",
		"not-a-url",
	}
	got := HTTPURLs(lines)
	if len(got) != 2 || got[0] != "https://s.weibo.com/weibo?q=%23a%23" || got[1] != "https://s.weibo.com/weibo?q=%23b%23" {
		t.Fatalf("HTTPURLs = %#v", got)
	}
	if FirstHTTPURL("#x\nhttps://example.com/g\n") != "https://example.com/g" {
		t.Fatal("FirstHTTPURL 应跳过注释")
	}
}
