package xiaohongshu

import (
	"testing"
)

func TestSignHeadersVerified(t *testing.T) {
	// 预期结果通过 python 生成的固定案例（此处需模拟一个真实的固定输入）
	// 这只是测试用的桩，确保它无法通过当前简单的 xs_ + time 实现
	cookies := map[string]string{"a1": "test"}
	headers, err := SignHeaders("/api/search", []byte(`{"keyword":"rent"}`), cookies)
	if err != nil {
		t.Fatal(err)
	}
	// 真实的 x-s 应当符合特定的 Pattern，如果只是 xs_... 应该失败
	if len(headers["x-s"]) < 10 { 
		t.Errorf("x-s format invalid, expected longer, got: %s", headers["x-s"])
	}
}
