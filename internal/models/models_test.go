package models

import "testing"

// 去重键 = 源 + 源内 ID（规格 3.1）
func TestRentPostDedupKey(t *testing.T) {
	p := RentPost{Source: "douban", ExternalID: "123"}
	if got, want := p.DedupKey(), "douban:123"; got != want {
		t.Fatalf("DedupKey() = %q, want %q", got, want)
	}
}

// 状态常量与状态机流转方向一致（规格 2.4）
func TestStatusConstants(t *testing.T) {
	flow := []string{PostStatusCollected, PostStatusPending, PostStatusPassed, PostStatusRejected, PostStatusSent, PostStatusAcked}
	for i, s := range flow {
		if s == "" {
			t.Fatalf("status[%d] 为空", i)
		}
	}
}
