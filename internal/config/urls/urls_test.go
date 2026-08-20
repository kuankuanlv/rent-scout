package urls

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

func TestWeiboUIDsAndContainerIDs(t *testing.T) {
	uids := WeiboUIDs([]string{
		"https://weibo.com/u/6342026928",
		"https://m.weibo.cn/u/6342026928?is_ori=1",
		"111",
		"# 注释",
		"https://weibo.com/u/6342026928",
	})
	if len(uids) != 2 || uids[0] != "6342026928" || uids[1] != "111" {
		t.Fatalf("UIDs=%v", uids)
	}
	cids := WeiboContainerIDs([]string{
		"https://weibo.com/p/100808453110d9ea6a7b6fd15e79788cf55186/super_index",
		"100808453110d9ea6a7b6fd15e79788cf55186_-_recommend",
		"# skip",
	})
	if len(cids) != 1 || cids[0] != "100808453110d9ea6a7b6fd15e79788cf55186" {
		t.Fatalf("CIDs=%v", cids)
	}
}
