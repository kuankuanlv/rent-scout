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
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	p := models.RentPost{Source: "douban", ExternalID: "done", Title: "t", CollectedAt: time.Now(), Status: models.PostStatusPassed}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}

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
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
		row := s.db.QueryRow(`SELECT id FROM posts WHERE external_id = ?`, fmt.Sprintf("m%d", i))
		var id int64
		if err := row.Scan(&id); err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatalf("建规则1: %v", err)
	}
	_, err = s.CreateRule(models.Rule{Name: "地铁近", Type: models.RuleTypeHardWhitelist, Mode: models.RuleModeInclude, Value: "地铁", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatalf("建规则2: %v", err)
	}
	r3, err := s.CreateRule(models.Rule{Name: "停用规则", Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "x", Enabled: false})
	if err != nil {
		t.Fatalf("建规则3: %v", err)
	}

	rules, err := s.ListRules(true) // 只取启用
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Errorf("启用规则 = %d, want 2", len(rules))
	}
	// 优先级降序：Priority 大的先执行（10 在 1 前）
	if rules[0].Priority != 10 || rules[1].Priority != 1 {
		t.Errorf("优先级排序错误: %+v", rules)
	}
	// 更新 + 删除
	if err := s.UpdateRule(models.Rule{ID: r1.ID, Name: "黑中介", Type: models.RuleTypeHardBlacklist, Mode: models.RuleModeExclude, Value: "中介,代理", Enabled: true, Priority: 10}); err != nil {
		t.Fatalf("更新: %v", err)
	}
	if err := s.DeleteRule(r3.ID); err != nil {
		t.Fatalf("删除: %v", err)
	}
	rules, _ = s.ListRules(false)
	if len(rules) != 2 {
		t.Errorf("删除后规则数 = %d, want 2", len(rules))
	}
}

// 通知插入幂等（postID+channel 唯一）+ 失败拉取 + 状态更新
func TestNotifications(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)

	if _, err := s.InsertNotification(postID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(postID, "feishu"); err != nil {
		t.Fatalf("重复插入应幂等: %v", err)
	}
	var cnt int
	s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE post_id=?`, postID).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("通知数 = %d, want 1", cnt)
	}

	// 标失败后可拉取重试，成功后不再拉取
	if err := s.MarkNotificationFailed(postID, "feishu", "webhook 403", 1); err != nil {
		t.Fatal(err)
	}
	pending, err := s.FetchPendingNotifications("feishu", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("失败待重试 = %d, want 1", len(pending))
	}
	if err := s.MarkNotificationSent(postID, "feishu"); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.FetchPendingNotifications("feishu", 10)
	if len(pending) != 0 {
		t.Errorf("发送后待重试 = %d, want 0", len(pending))
	}
}

// 反馈写入 + 游标读写
func TestFeedbackAndCursor(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)

	if err := s.InsertFeedback(models.Feedback{PostID: postID, Channel: "feishu", Action: models.FeedbackUseless, Reason: "价格虚假"}); err != nil {
		t.Fatalf("写反馈: %v", err)
	}
	if err := s.SetCursor("douban", "page:3"); err != nil {
		t.Fatalf("写游标: %v", err)
	}
	cursor, ok, err := s.GetCursor("douban")
	if err != nil || !ok {
		t.Fatalf("读游标: ok=%v err=%v", ok, err)
	}
	if cursor != "page:3" {
		t.Errorf("游标 = %q, want page:3", cursor)
	}
}

// 辅助：插入一条帖子返回 id
func seedPost(t *testing.T, s *Store) int64 {
	t.Helper()
	p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("seed-%d", time.Now().UnixNano()), Title: "t", CollectedAt: time.Now(), Status: models.PostStatusPassed}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
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

// K2（最终审查）：AI 判定帖 hard_rules 为 null/空 → RuleHitStats 不产生 rule_id=0 幽灵统计行。
// 双保险验证：SaveFilterResult 归一化（nil→[]）+ 统计侧 WHERE hr.value IS NOT NULL 过滤老库 "null" 残留
func TestRuleHitStatsAIPostsNoGhost(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// 一条正常 hard 命中（规则1，应统计）
	p1 := seedPost(t, s)
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: p1, Status: models.PostStatusPassed, Stage: models.StageHardRule,
		DecidedAt: time.Now(),
		HardRules: []models.RuleHit{{RuleID: 1, Mode: models.RuleModeInclude, Reason: "望京"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 一条 AI 判定帖（hard_rules nil → 归一化为 "[]"，不得产生统计行）
	p2 := seedPost(t, s)
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: p2, Status: models.PostStatusPassed, Stage: models.StageAIRule,
		DecidedAt: time.Now(), AI: &models.AIResult{Passed: true, Reason: "近地铁"},
	}); err != nil {
		t.Fatal(err)
	}
	// 归一化断言：库里存的是 "[]" 而非 "null"
	var raw string
	if err := s.db.QueryRow(`SELECT hard_rules FROM filter_results WHERE post_id=?`, p2).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "[]" {
		t.Errorf("AI 帖 hard_rules = %q, want \"[]\"（nil 归一化）", raw)
	}
	// 一条老库残留 hard_rules="null" 的 passed 帖（统计侧 WHERE 过滤，双保险）
	p3 := seedPost(t, s)
	if err := s.SaveFilterResult(models.FilterResult{PostID: p3, Status: models.PostStatusPassed,
		Stage: models.StageAIRule, DecidedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE filter_results SET hard_rules='null' WHERE post_id=?`, p3); err != nil {
		t.Fatal(err)
	}

	stats, err := s.RuleHitStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].RuleID != 1 {
		t.Fatalf("统计 = %+v, want 仅规则1（无 rule_id=0 幽灵行）", stats)
	}
	if stats[0].Hits != 1 {
		t.Errorf("hits = %d, want 1", stats[0].Hits)
	}
}

// 帖子列表：状态过滤 / 分页 / 空 status 全量（id 倒序）
func TestListPosts(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// 3 帖不同状态（播种顺序 = id 升序）
	statuses := []string{models.PostStatusCollected, models.PostStatusPassed, models.PostStatusRejected}
	for i, st := range statuses {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("lp%d", i), Title: "t",
			CollectedAt: time.Now(), Status: st}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	// 空 status：全量 3 帖，id 倒序（rejected 最后播种 id 最大 → 排最前）
	all, err := s.ListPosts("", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("全量 = %d, want 3", len(all))
	}
	if all[0].Status != models.PostStatusRejected {
		t.Errorf("倒序错误: 首帖状态 = %s, want rejected（id 最大）", all[0].Status)
	}

	// 按状态过滤
	passed, err := s.ListPosts(models.PostStatusPassed, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(passed) != 1 || passed[0].Status != models.PostStatusPassed {
		t.Errorf("过滤 passed = %+v, want 仅 1 帖", passed)
	}

	// 分页：limit=1 offset=1 → 第二新的一帖（passed）
	page, err := s.ListPosts("", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != models.PostStatusPassed {
		t.Errorf("分页 = %+v, want 1 帖 passed", page)
	}
}

// 帖子详情：存在 ok=true；不存在 ok=false
func TestGetPost(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id := seedPost(t, s)

	p, ok, err := s.GetPost(id)
	if err != nil || !ok {
		t.Fatalf("详情: ok=%v err=%v", ok, err)
	}
	if p.ID != id || p.Source != "douban" {
		t.Errorf("详情内容: %+v", p)
	}
	_, ok, err = s.GetPost(id + 999)
	if err != nil || ok {
		t.Errorf("不存在应 ok=false: ok=%v err=%v", ok, err)
	}
}

// 帖子详情页：通知记录 + 反馈记录
func TestPostDetailLists(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id := seedPost(t, s)

	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(id, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFeedback(models.Feedback{PostID: id, Channel: "feishu", Action: models.FeedbackUseless, Reason: "假房源"}); err != nil {
		t.Fatal(err)
	}

	notifs, err := s.ListNotificationsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 2 || notifs[0].PostID != id {
		t.Errorf("帖子通知 = %+v, want 2 条", notifs)
	}
	if notifs[0].Status != models.NotifyStatusPending {
		t.Errorf("通知初始状态 = %s, want pending", notifs[0].Status)
	}

	feedbacks, err := s.ListFeedbacksByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedbacks) != 1 || feedbacks[0].Action != models.FeedbackUseless {
		t.Errorf("帖子反馈 = %+v, want 1 条 useless", feedbacks)
	}
}

// 死信重发：dead → pending 且清空次数/错误；非 dead 幂等返回 false
func TestResetNotification(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	deadID := seedPost(t, s)
	sentID := seedPost(t, s)

	if _, err := s.InsertNotification(deadID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationFailed(deadID, "feishu", "webhook 403", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(deadID, "feishu", "webhook 403"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}

	// dead 帖 reset → true，状态变 pending，attempts/last_error 清零
	reset, err := s.ResetNotification(deadID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if !reset {
		t.Error("dead reset 应返回 true")
	}
	var status string
	var attempts int
	var lastErr string
	if err := s.db.QueryRow(`SELECT status, attempts, last_error FROM notifications WHERE post_id=? AND channel='feishu'`, deadID).
		Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != models.NotifyStatusPending || attempts != 0 || lastErr != "" {
		t.Errorf("reset 后 = %s/%d/%q, want pending/0/''", status, attempts, lastErr)
	}
	// 再次 reset 已非 dead → false（幂等）
	reset, err = s.ResetNotification(deadID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Error("非 dead 再次 reset 应返回 false")
	}
	// sent 帖 reset → false
	reset, err = s.ResetNotification(sentID, "feishu")
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Error("sent 帖 reset 应返回 false")
	}
}

// 死信列表：只返回 dead，id 倒序
func TestFetchDeadNotifications(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	deadID := seedPost(t, s)
	sentID := seedPost(t, s)
	pendingID := seedPost(t, s)

	if _, err := s.InsertNotification(deadID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(deadID, "feishu", "403"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(sentID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(pendingID, "feishu"); err != nil {
		t.Fatal(err)
	}

	dead, err := s.FetchDeadNotifications(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].PostID != deadID || dead[0].Status != models.NotifyStatusDead {
		t.Errorf("死信列表 = %+v, want 仅 dead 帖 %d", dead, deadID)
	}
}

// 反馈列表：id 倒序 + 限量
func TestListFeedbacks(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id1 := seedPost(t, s)
	id2 := seedPost(t, s)
	if err := s.InsertFeedback(models.Feedback{PostID: id1, Channel: "feishu", Action: models.FeedbackUseless, Reason: "假房源"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFeedback(models.Feedback{PostID: id2, Channel: "feishu", Action: models.FeedbackUseful, Reason: "真实"}); err != nil {
		t.Fatal(err)
	}

	feedbacks, err := s.ListFeedbacks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedbacks) != 2 {
		t.Errorf("反馈数 = %d, want 2", len(feedbacks))
	}
	if feedbacks[0].PostID != id2 {
		t.Errorf("倒序错误: 首条 post_id = %d, want %d（后写入）", feedbacks[0].PostID, id2)
	}
	if feedbacks[1].Action != models.FeedbackUseless {
		t.Errorf("反馈内容: %+v", feedbacks[1])
	}
	// 限量
	one, err := s.ListFeedbacks(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Errorf("限量 = %d, want 1", len(one))
	}
}

// 今日统计：只统计今日 collected 与今日判定分布，昨日不计入
func TestTodayStats(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	now := time.Now()
	// 今日 2 帖 + 昨日 1 帖（AddDate 保证日期偏移一天，跨午夜安全）
	for i := 0; i < 2; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("td%d", i), Title: "t",
			CollectedAt: now, Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	y := models.RentPost{Source: "douban", ExternalID: "yesterday", Title: "t",
		CollectedAt: now.AddDate(0, 0, -1), Status: models.PostStatusCollected}
	if _, err := s.InsertPost(y); err != nil {
		t.Fatal(err)
	}

	// 3 条昨日采集的帖子（避免污染今日 collected 计数），分别做今日/昨日判定
	var ids []int64
	for i := 0; i < 3; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("fr%d", i), Title: "t",
			CollectedAt: now.AddDate(0, 0, -1), Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM posts WHERE external_id=?`, fmt.Sprintf("fr%d", i)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: ids[0], Status: models.PostStatusPassed, Stage: models.StageHardRule, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: ids[1], Status: models.PostStatusRejected, Stage: models.StageHardRule, RejectedBy: "x", DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: ids[2], Status: models.PostStatusPassed, Stage: models.StageHardRule, DecidedAt: now.AddDate(0, 0, -1)}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.TodayStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Collected != 2 {
		t.Errorf("Collected = %d, want 2", stats.Collected)
	}
	if stats.Passed != 1 {
		t.Errorf("Passed = %d, want 1", stats.Passed)
	}
	if stats.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", stats.Rejected)
	}
	if stats.Pending != 0 {
		t.Errorf("Pending = %d, want 0", stats.Pending)
	}
}

// 渠道发送统计：多渠道多状态聚合（历史全量）
func TestChannelStats(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// feishu: sent×2 + dead×1；pushplus: failed×1；dingtalk: pending×1
	feishuPosts := []int64{seedPost(t, s), seedPost(t, s), seedPost(t, s)}
	for _, id := range feishuPosts[:2] {
		if _, err := s.InsertNotification(id, "feishu"); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkNotificationSent(id, "feishu"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertNotification(feishuPosts[2], "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(feishuPosts[2], "feishu", "403"); err != nil {
		t.Fatal(err)
	}

	pp := seedPost(t, s)
	if _, err := s.InsertNotification(pp, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationFailed(pp, "pushplus", "timeout", 1); err != nil {
		t.Fatal(err)
	}

	dt := seedPost(t, s)
	if _, err := s.InsertNotification(dt, "dingtalk"); err != nil {
		t.Fatal(err)
	}

	stats, err := s.ChannelStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 3 {
		t.Fatalf("渠道数 = %d, want 3: %+v", len(stats), stats)
	}
	got := map[string]ChannelStat{}
	for _, st := range stats {
		got[st.Channel] = st
	}
	if g := got["feishu"]; g.Sent != 2 || g.Failed != 0 || g.Dead != 1 {
		t.Errorf("feishu 统计 = %+v, want sent=2 failed=0 dead=1", g)
	}
	if g := got["pushplus"]; g.Sent != 0 || g.Failed != 1 || g.Dead != 0 {
		t.Errorf("pushplus 统计 = %+v, want sent=0 failed=1 dead=0", g)
	}
	if g := got["dingtalk"]; g.Sent != 0 || g.Failed != 0 || g.Dead != 0 {
		t.Errorf("dingtalk 统计 = %+v, want 全 0（仅 pending）", g)
	}
}
