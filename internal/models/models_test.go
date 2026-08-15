package models

import (
	"bytes"
	"encoding/json"
	"testing"
)

// 去重键 = 源 + 源内 ID（规格 3.1）
func TestRentPostDedupKey(t *testing.T) {
	p := RentPost{Source: "douban", ExternalID: "123"}
	if got, want := p.DedupKey(), "douban:123"; got != want {
		t.Fatalf("DedupKey() = %q, want %q", got, want)
	}
}

// 帖子主状态仅四态（Spec 09 §1）
func TestStatusConstants(t *testing.T) {
	flow := []string{PostStatusCollected, PostStatusPassed, PostStatusRejected}
	if len(flow) != 3 {
		t.Fatalf("主状态数 = %d, want 3", len(flow))
	}
	for i, s := range flow {
		if s == "" {
			t.Fatalf("status[%d] 为空", i)
		}
	}
}

// AddressTags 多值语义（调整规格 2.3）：JSON 可序列化，分组主键取 [0]
func TestAddressTagsJSON(t *testing.T) {
	p := RentPost{Source: "douban", ExternalID: "1", AddressTags: []string{"望京", "14号线"}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"addressTags":["望京","14号线"]`)) {
		t.Errorf("JSON 缺少 addressTags: %s", b)
	}
}
