package store

import (
	"database/sql"
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

// 多状态拉批：collected+pending 混合
func TestFetchPendingByStatuses(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("s%d", i), Title: "t",
			CollectedAt: time.Now(), Status: models.PostStatusCollected}
		s.InsertPost(p)
	}
	// 一条 pending
	p := models.RentPost{Source: "douban", ExternalID: "pending-1", Title: "t",
		CollectedAt: time.Now(), Status: models.PostStatusPending}
	s.InsertPost(p)
	// 一条 passed（不应被拉取）
	q := models.RentPost{Source: "douban", ExternalID: "passed-1", Title: "t",
		CollectedAt: time.Now(), Status: models.PostStatusPassed}
	s.InsertPost(q)

	batch, err := s.FetchPendingByStatuses([]string{models.PostStatusCollected, models.PostStatusPending}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 4 {
		t.Errorf("批数 = %d, want 4", len(batch))
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

// AddressTags 读写往返：插入后拉回，标签保持（调整规格 2.3）
func TestAddressTagsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	p := models.RentPost{Source: "douban", ExternalID: "tag-1", Title: "t",
		CollectedAt: time.Now(), Status: models.PostStatusCollected,
		AddressTags: []string{"望京", "14号线"}}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	batch, err := s.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 {
		t.Fatalf("批数 = %d, want 1", len(batch))
	}
	if got := batch[0].AddressTags; len(got) != 2 || got[0] != "望京" {
		t.Errorf("AddressTags = %v, want [望京 14号线]", got)
	}
}

// 已有库（无 address_tags 列）重复 Open：ALTER 补列，数据不丢
func TestMigrateAddsColumnToLegacyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	// 先建旧版 posts 表（无 address_tags 列）
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE posts (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    source TEXT NOT NULL, external_id TEXT NOT NULL,
	    url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
	    content TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '',
	    author_url TEXT NOT NULL DEFAULT '', published_at DATETIME,
	    collected_at DATETIME NOT NULL, status TEXT NOT NULL DEFAULT 'collected',
	    raw TEXT NOT NULL DEFAULT '', UNIQUE(source, external_id)
	)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	// 用新版本 Open：应自动 ALTER 补列
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 旧库失败: %v", err)
	}
	defer s.Close()
	ok, err := s.columnExists("posts", "address_tags")
	if err != nil || !ok {
		t.Fatalf("旧库补列失败: ok=%v err=%v", ok, err)
	}
}

// 批量查重：只返回已存在的 ID 集合；空输入不报错
func TestExistsByExternalIDs(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("e%d", i), Title: "t",
			CollectedAt: time.Now(), Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	// 混合：2 个存在 + 1 个不存在 + 1 个其他源的同名 ID
	ids := []string{"e0", "e2", "nope", "x"}
	existing, err := s.ExistsByExternalIDs("douban", ids)
	if err != nil {
		t.Fatal(err)
	}
	if !existing["e0"] || !existing["e2"] {
		t.Errorf("e0/e2 应存在: %v", existing)
	}
	if existing["nope"] || existing["x"] {
		t.Errorf("nope/x 不应存在: %v", existing)
	}
	// 空输入
	empty, err := s.ExistsByExternalIDs("douban", nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("空输入: %v %v", empty, err)
	}
}

// 筛选结果 upsert：同帖重复判定覆盖（1:1 posts，规格 3.2）
func TestSaveFilterResult(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)

	r1 := models.FilterResult{
		PostID: postID, Status: models.PostStatusRejected, Stage: models.StageHardRule,
		RejectedBy: "黑名单命中:中介", DecidedAt: time.Now(),
		HardRules: []models.RuleHit{{RuleID: 3, Mode: models.RuleModeExclude, Reason: "中介"}},
	}
	if err := s.SaveFilterResult(r1); err != nil {
		t.Fatalf("首次保存: %v", err)
	}
	// 同帖再次判定（AI 复核通过场景）：覆盖
	r2 := models.FilterResult{PostID: postID, Status: models.PostStatusPassed,
		Stage: models.StageAIRule, DecidedAt: time.Now(),
		AI: &models.AIResult{Passed: true, Reason: "近地铁", Price: 4500, Confidence: 0.9}}
	if err := s.SaveFilterResult(r2); err != nil {
		t.Fatalf("覆盖保存: %v", err)
	}
	got, ok, err := s.FilterResultByPostID(postID)
	if err != nil || !ok {
		t.Fatalf("回读: ok=%v err=%v", ok, err)
	}
	if got.Status != models.PostStatusPassed || got.Stage != models.StageAIRule {
		t.Errorf("覆盖未生效: %+v", got)
	}
	if got.AI == nil || got.AI.Price != 4500 || !got.AI.Passed {
		t.Errorf("AI 详情丢失: %+v", got.AI)
	}
}

// 地址标签写回：白名单命中后入库（调整规格 A）
func TestUpdatePostAddressTags(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)
	if err := s.UpdatePostAddressTags(postID, []string{"望京", "14号线"}); err != nil {
		t.Fatal(err)
	}
	// seedPost 建的是 passed 状态帖子，按该状态回读验证写回
	batch, err := s.FetchPendingByStatus(models.PostStatusPassed, 10)
	if err != nil || len(batch) != 1 {
		t.Fatalf("回读失败: %v %d", err, len(batch))
	}
	if len(batch[0].AddressTags) != 2 || batch[0].AddressTags[0] != "望京" {
		t.Errorf("标签未写回: %v", batch[0].AddressTags)
	}
}

// 规则命中统计：passed 帖子的 hard_rules 按规则聚合 + 负向反馈归因
func TestRuleHitStats(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// 两条 passed 帖子命中同一规则；其一被标 useless
	p1 := seedPost(t, s)
	p2 := seedPost(t, s)
	for _, id := range []int64{p1, p2} {
		if err := s.SaveFilterResult(models.FilterResult{
			PostID: id, Status: models.PostStatusPassed, Stage: models.StageHardRule,
			DecidedAt: time.Now(),
			HardRules: []models.RuleHit{{RuleID: 1, Mode: models.RuleModeInclude, Reason: "望京"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertFeedback(models.Feedback{PostID: p1, Channel: "feishu", Action: models.FeedbackUseless, Reason: "假房源"}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.RuleHitStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].RuleID != 1 {
		t.Fatalf("统计 = %+v, want 规则1", stats)
	}
	if stats[0].Hits != 2 || stats[0].UselessCount != 1 {
		t.Errorf("统计错误: hits=%d useless=%d, want 2/1", stats[0].Hits, stats[0].UselessCount)
	}
}
