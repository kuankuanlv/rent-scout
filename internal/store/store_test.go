package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/models"
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

// 去重插入：同 DedupKey 只入一条，返回是否新增
func TestInsertPostDedup(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	p := models.RentPost{
		Source: "douban", ExternalID: "101", Title: "望京整租",
		URL: "https://example.com/101", CollectedAt: time.Now(), Status: models.PostStatusCollected,
	}
	added, err := s.InsertPost(p)
	if err != nil {
		t.Fatalf("首次插入: %v", err)
	}
	if !added {
		t.Fatal("首次插入应返回 added=true")
	}
	added, err = s.InsertPost(p)
	if err != nil {
		t.Fatalf("重复插入: %v", err)
	}
	if added {
		t.Fatal("重复插入应返回 added=false")
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("帖子数 = %d, want 1", cnt)
	}
}

// 拉取待筛选批：只拉 collected，限量，按 id 升序
func TestFetchPending(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// 3 条 collected + 1 条已 passed
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("p%d", i), Title: "t", CollectedAt: time.Now(), Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil { t.Fatal(err) }
	}
	p := models.RentPost{Source: "douban", ExternalID: "done", Title: "t", CollectedAt: time.Now(), Status: models.PostStatusPassed}
	if _, err := s.InsertPost(p); err != nil { t.Fatal(err) }

	batch, err := s.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 3 {
		t.Errorf("待筛选批 = %d, want 3", len(batch))
	}
	batch2, err := s.FetchPendingByStatus(models.PostStatusCollected, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch2) != 2 {
		t.Errorf("限量批 = %d, want 2", len(batch2))
	}
}

// 批量状态流转：原子更新一批帖子的主状态
func TestMarkStatus(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	ids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("m%d", i), Title: "t", CollectedAt: time.Now(), Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil { t.Fatal(err) }
		row := s.db.QueryRow(`SELECT id FROM posts WHERE external_id = ?`, fmt.Sprintf("m%d", i))
		var id int64
		if err := row.Scan(&id); err != nil { t.Fatal(err) }
		ids = append(ids, id)
	}
	if err := s.MarkStatus(ids, models.PostStatusPending); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE status = 'pending'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Errorf("pending 数 = %d, want 3", cnt)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 规则 CRUD + 按启用过滤 + 优先级排序
func TestRulesCRUD(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	r1, err := s.CreateRule(models.Rule{Name: "黑中介", Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介", Enabled: true, Priority: 10})
	if err != nil { t.Fatalf("建规则1: %v", err) }
	_, err = s.CreateRule(models.Rule{Name: "地铁近", Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "地铁", Enabled: true, Priority: 1})
	if err != nil { t.Fatalf("建规则2: %v", err) }
	r3, err := s.CreateRule(models.Rule{Name: "停用规则", Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "x", Enabled: false})
	if err != nil { t.Fatalf("建规则3: %v", err) }

	rules, err := s.ListRules(true) // 只取启用
	if err != nil { t.Fatal(err) }
	if len(rules) != 2 { t.Errorf("启用规则 = %d, want 2", len(rules)) }
	// 优先级降序：Priority 大的先执行（10 在 1 前）
	if rules[0].Priority != 10 || rules[1].Priority != 1 {
		t.Errorf("优先级排序错误: %+v", rules)
	}
	// 更新 + 删除
	if err := s.UpdateRule(models.Rule{ID: r1.ID, Name: "黑中介", Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介,代理", Enabled: true, Priority: 10}); err != nil {
		t.Fatalf("更新: %v", err)
	}
	if err := s.DeleteRule(r3.ID); err != nil { t.Fatalf("删除: %v", err) }
	rules, _ = s.ListRules(false)
	if len(rules) != 2 { t.Errorf("删除后规则数 = %d, want 2", len(rules)) }
}

// 通知插入幂等（postID+channel 唯一）+ 失败拉取 + 状态更新
func TestNotifications(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)

	if _, err := s.InsertNotification(postID, "feishu"); err != nil { t.Fatal(err) }
	if _, err := s.InsertNotification(postID, "feishu"); err != nil {
		t.Fatalf("重复插入应幂等: %v", err)
	}
	var cnt int
	s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE post_id=?`, postID).Scan(&cnt)
	if cnt != 1 { t.Errorf("通知数 = %d, want 1", cnt) }

	// 标失败后可拉取重试，成功后不再拉取
	if err := s.MarkNotificationFailed(postID, "feishu", "webhook 403", 1); err != nil { t.Fatal(err) }
	pending, err := s.FetchPendingNotifications("feishu", 10)
	if err != nil { t.Fatal(err) }
	if len(pending) != 1 { t.Errorf("失败待重试 = %d, want 1", len(pending)) }
	if err := s.MarkNotificationSent(postID, "feishu"); err != nil { t.Fatal(err) }
	pending, _ = s.FetchPendingNotifications("feishu", 10)
	if len(pending) != 0 { t.Errorf("发送后待重试 = %d, want 0", len(pending)) }
}

// 反馈写入 + 游标读写
func TestFeedbackAndCursor(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)

	if err := s.InsertFeedback(models.Feedback{PostID: postID, Channel: "feishu", Action: models.FeedbackUseless, Reason: "价格虚假"}); err != nil {
		t.Fatalf("写反馈: %v", err)
	}
	if err := s.SetCursor("douban", "page:3"); err != nil { t.Fatalf("写游标: %v", err) }
	cursor, ok, err := s.GetCursor("douban")
	if err != nil || !ok { t.Fatalf("读游标: ok=%v err=%v", ok, err) }
	if cursor != "page:3" { t.Errorf("游标 = %q, want page:3", cursor) }
}

// 辅助：插入一条帖子返回 id
func seedPost(t *testing.T, s *Store) int64 {
	t.Helper()
	p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("seed-%d", time.Now().UnixNano()), Title: "t", CollectedAt: time.Now(), Status: models.PostStatusPassed}
	if _, err := s.InsertPost(p); err != nil { t.Fatal(err) }
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM posts WHERE source='douban' ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
