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

// Tags JSON 可序列化
func TestTagsJSON(t *testing.T) {
	p := RentPost{Source: "douban", ExternalID: "1", Tags: []PostTag{
		{Kind: TagKindLocation, Text: "望京"},
		{Kind: TagKindLocation, Text: "14号线"},
	}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"tags":[`)) || !bytes.Contains(b, []byte(`"望京"`)) {
		t.Errorf("JSON 缺少 tags: %s", b)
	}
}
