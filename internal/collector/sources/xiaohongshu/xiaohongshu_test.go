package xiaohongshu

import (
	"testing"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

func TestXiaohongshu(t *testing.T) {
	s := &Xiaohongshu{}
	if s.Name() != models.SourceXiaohongshu.String() {
		t.Errorf("expected source name %s, got %s", models.SourceXiaohongshu.String(), s.Name())
	}

	// 测试 Fingerprint 规范
	cfg := &config.AppConfig{}
	fp1 := s.Fingerprint(cfg)
	if fp1 == "" {
		t.Error("fingerprint should not be empty")
	}

	// 测试 Iterator 接口
	it := s.NewIterator("some-target", time.Now().Add(-time.Hour), time.Now())
	if it == nil {
		t.Fatal("iterator should not be nil")
	}

	ok := false
	var _ collector.Iterator = it
	ok = true
	if !ok {
		t.Fatal("it does not implement collector.Iterator")
	}

	// 测试 Iterator 状态检查
	err := it.Err()
	if err != nil {
		t.Errorf("expected no error initially, got %v", err)
	}
}
