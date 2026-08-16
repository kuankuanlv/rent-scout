package models

import "testing"

func TestExtractContact(t *testing.T) {
	cases := []struct {
		title, content, want string
	}{
		{"望京整租", "加微信 abc_house 随时看房", "abc_house"},
		{"梨园", "电话13812345678，微信同号", "13812345678"},
		{"回龙观", "邮箱 rent@example.com 联系", "rent@example.com"},
		{"合租", "QQ：12345678", "QQ:12345678"},
		{"无联系", "近地铁拎包入住", ContactUnknown},
		{"组合", "微信 wx_user，手机 13900001111，mail a@b.com", "wx_user / 13900001111 / a@b.com"},
		{"带分隔符", "电话 138-1234-5678 微信同号", "13812345678"},
		{"全角手机", "手机号：１３９００００１１１１", "13900001111"},
		{"中文数字手机", "电联 一三八一二三四五六七八", "13812345678"},
		{"加v", "加v：house88ok 随时看", "house88ok"},
		{"微信数字号", "微信：13812345678", "13812345678"},
	}
	for _, c := range cases {
		got := ExtractContact(c.title, c.content)
		if got != c.want {
			t.Errorf("title=%q content=%q got %q want %q", c.title, c.content, got, c.want)
		}
	}
}

func TestFillPostContactKeepsExplicit(t *testing.T) {
	p := RentPost{Content: "微信 abcdefg", Contact: "已填"}
	FillPostContact(&p)
	if p.Contact != "已填" {
		t.Errorf("已有联系方式不应覆盖: %s", p.Contact)
	}
}
