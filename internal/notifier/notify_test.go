package notifier

import (
	"context"
	"testing"
)

// 渠道常量完整性：六渠道 + 未分组标签
func TestChannelConstants(t *testing.T) {
	want := []string{ChannelFeishu, ChannelDingtalk, ChannelWecom, ChannelPushplus, ChannelServerchan, ChannelWebhook}
	if len(want) != 6 {
		t.Fatalf("渠道数量: %d", len(want))
	}
	for _, c := range want {
		if c == "" {
			t.Error("渠道名为空")
		}
	}
	if GroupUnknown != "未分组" {
		t.Errorf("未分组标签: %q", GroupUnknown)
	}
}

// NotifyItem 组装：完整字段
func TestNotifyItem(t *testing.T) {
	item := NotifyItem{
		PostID: 1, Title: "望京整租", URL: "https://x", Price: 4500,
		Contact: "张先生", Commuting: "14号线望京站", Reason: "近地铁",
		AddressTag: "望京", FeedbackURL: "/f?post=1", HandledURL: "/h?post=1",
	}
	if item.PostID != 1 || item.AddressTag != "望京" || item.HandledURL == "" {
		t.Errorf("字段丢失: %+v", item)
	}
}

// Channel 接口签名：Name + Send（编译期断言）
func TestChannelInterface(t *testing.T) {
	var _ Channel = &fakeChannel{}
}

type fakeChannel struct{}

func (f *fakeChannel) Name() string { return "fake" }
func (f *fakeChannel) Send(ctx context.Context, items []NotifyItem) ([]int64, []error, error) {
	return []int64{}, nil, nil
}
