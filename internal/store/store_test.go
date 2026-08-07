package store

import (
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	// 迁移后 6 张表全部存在（规格 3.5）
	tables := []string{"posts", "filter_results", "rules", "notifications", "feedbacks", "source_state"}
	for _, name := range tables {
		var cnt int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&cnt); err != nil {
			t.Fatalf("查表 %s: %v", name, err)
		}
		if cnt != 1 {
			t.Errorf("表 %s 不存在", name)
		}
	}
}

// 重复 Open 不报错（幂等迁移）；WAL 开启
func TestOpenTwice(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("重复 Open 失败: %v", err)
	}
	defer s2.Close()
	var journal string
	if err := s2.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
}
