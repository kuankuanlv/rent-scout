package weibo

import (
	"context"
	"testing"
)

func TestWeiboStub(t *testing.T) {
	s := New(nil)
	if s.Name() != "weibo" {
		t.Fatalf("name=%s", s.Name())
	}
	items, next, err := s.List(context.Background(), "")
	if err != nil || next != "" || len(items) != 0 {
		t.Fatalf("占位 List 应空: items=%d next=%q err=%v", len(items), next, err)
	}
}
