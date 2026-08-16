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

func TestWeiboTagsFromMixedLines(t *testing.T) {
	got := WeiboTags([]string{
		"#北京租房",
		"https://s.weibo.com/weibo?q=%23%E5%8C%97%E4%BA%AC%E7%A7%9F%E6%88%BF%23",
		"# 这是注释",
		"租房",
		"",
	})
	if len(got) != 2 || got[0] != "#北京租房#" || got[1] != "#租房#" {
		t.Fatalf("WeiboTags = %#v", got)
	}
}
