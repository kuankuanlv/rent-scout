package store

import (
	"database/sql"
	"errors"
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
	tables := []string{"posts", "filter_results", "rules", "notifications", "post_tags", "source_state"}
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
	got, ok, err := s.GetPost(1)
	if err != nil || !ok {
		t.Fatalf("GetPost: ok=%v err=%v", ok, err)
	}
	if got.Price != models.PriceUnknown {
		t.Errorf("无价格帖 Price=%q want %q", got.Price, models.PriceUnknown)
	}
	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "102", Title: "梨园月租3800",
		CollectedAt: time.Now(), Status: models.PostStatusCollected,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListPosts(PostListFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var priced models.RentPost
	for _, x := range list {
		if x.ExternalID == "102" {
			priced = x
		}
	}
	if priced.Price != "3800" {
		t.Errorf("正则价格 = %q want 3800 (id=%d)", priced.Price, priced.ID)
	}
}

func TestListFilterTags(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "t1", Title: "a", CollectedAt: time.Now(),
		Status: models.PostStatusPassed,
	}); err != nil {
		t.Fatal(err)
	}
	var passID int64
	_ = s.db.QueryRow(`SELECT id FROM posts WHERE external_id='t1'`).Scan(&passID)
	if err := s.ReplaceSystemTags(passID, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
		{Kind: models.TagKindLocation, Text: "14号线", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	rej := models.RentPost{
		Source: "douban", ExternalID: "t2", Title: "中介房", CollectedAt: time.Now(),
		Status: models.PostStatusRejected,
	}
	if _, err := s.InsertPost(rej); err != nil {
		t.Fatal(err)
	}
	id := int64(0)
	_ = s.db.QueryRow(`SELECT id FROM posts WHERE external_id='t2'`).Scan(&id)
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: id, Status: models.PostStatusRejected, Stage: models.StageHardRule,
		RejectedBy: "黑名单命中:中介", HardRules: []models.RuleHit{{RuleID: 1, Reason: "中介"}},
		DecidedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(id, []models.PostTag{
		{Kind: models.TagKindBlock, Text: "中介", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	def := models.RentPost{
		Source: "douban", ExternalID: "t3", Title: "无关", CollectedAt: time.Now(),
		Status: models.PostStatusRejected,
	}
	if _, err := s.InsertPost(def); err != nil {
		t.Fatal(err)
	}
	var defID int64
	_ = s.db.QueryRow(`SELECT id FROM posts WHERE external_id='t3'`).Scan(&defID)
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: defID, Status: models.PostStatusRejected, Stage: models.StageHardRule,
		RejectedBy: models.RejectedByUnmatched, DecidedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(defID, []models.PostTag{
		{Kind: models.TagKindUnmatched, Text: models.RejectedByUnmatched, Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	passAI := models.RentPost{
		Source: "douban", ExternalID: "t4", Title: "ai", CollectedAt: time.Now(),
		Status: models.PostStatusPassed,
	}
	if _, err := s.InsertPost(passAI); err != nil {
		t.Fatal(err)
	}
	var aiID int64
	_ = s.db.QueryRow(`SELECT id FROM posts WHERE external_id='t4'`).Scan(&aiID)
	if err := s.SaveFilterResult(models.FilterResult{
		PostID: aiID, Status: models.PostStatusPassed, Stage: models.StageAIRule,
		DecidedAt: time.Now(), AI: &models.AIResult{Passed: true, Reason: "合适"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(aiID, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListFilterTags()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"望京": true, "14号线": true, "中介": true, models.RejectedByUnmatched: true}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v（不含 AI 徽章）", got, want)
	}
	for _, ft := range got {
		if !want[ft.Text] {
			t.Errorf("多余标签 %q", ft.Text)
		}
	}

	byBlack, err := s.ListPosts(PostListFilter{Tag: "中介"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byBlack) != 1 || byBlack[0].ExternalID != "t2" {
		t.Fatalf("按黑名单标签筛选 = %+v", byBlack)
	}
	byAI, err := s.ListPosts(PostListFilter{AI: "pass"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byAI) != 1 || byAI[0].ExternalID != "t4" {
		t.Fatalf("按 AI 独立条件筛选 = %+v", byAI)
	}
	byUnmatched, err := s.ListPosts(PostListFilter{Tag: models.RejectedByUnmatched}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUnmatched) != 1 || byUnmatched[0].ExternalID != "t3" {
		t.Fatalf("按未命中标签筛选 = %+v", byUnmatched)
	}
}

func TestReconstructKVAfter(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	if err := SetConfig(s, "log.level", "info"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfig(s, "log.level", "debug"); err != nil {
		t.Fatal(err)
	}
	hist, err := ListConfigHistory(s, 10)
	if err != nil || len(hist) < 2 {
		t.Fatalf("history n=%d err=%v", len(hist), err)
	}
	// hist[0] 最新 debug；hist[1] 更早 info
	var infoID, debugID int64
	for _, e := range hist {
		switch {
		case e.Key == "log.level" && e.NewValue == "info":
			infoID = e.ID
		case e.Key == "log.level" && e.NewValue == "debug":
			debugID = e.ID
		}
	}
	if infoID == 0 || debugID == 0 || infoID >= debugID {
		t.Fatalf("ids info=%d debug=%d", infoID, debugID)
	}

	kv, _, err := ReconstructKVAfter(s, infoID)
	if err != nil {
		t.Fatal(err)
	}
	if kv["log.level"] != "info" {
		t.Errorf("回放到 info 后 = %q, want info", kv["log.level"])
	}
	kv, _, err = ReconstructKVAfter(s, debugID)
	if err != nil {
		t.Fatal(err)
	}
	if kv["log.level"] != "debug" {
		t.Errorf("回放到 debug 后 = %q, want debug", kv["log.level"])
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
	if err := s.MarkStatus(ids, models.PostStatusPassed); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE status = 'passed'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Errorf("passed 数 = %d, want 3", cnt)
	}
}

// MarkStatus / InsertPost 拒写已废弃 sent/acked（Spec 09 §1）
func TestPostStatusRejectSentAcked(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	p := models.RentPost{Source: "douban", ExternalID: "ok1", Title: "t", CollectedAt: time.Now(), Status: models.PostStatusCollected}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM posts WHERE external_id = ?`, "ok1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"sent", "acked"} {
		if err := s.MarkStatus([]int64{id}, bad); err == nil {
			t.Fatalf("MarkStatus(%s) 应失败", bad)
		}
		var st string
		if err := s.db.QueryRow(`SELECT status FROM posts WHERE id = ?`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st != models.PostStatusCollected {
			t.Fatalf("拒写后 status = %q, want collected", st)
		}
		_, err := s.InsertPost(models.RentPost{
			Source: "douban", ExternalID: "bad-" + bad, Title: "t",
			CollectedAt: time.Now(), Status: bad,
		})
		if err == nil {
			t.Fatalf("InsertPost(%s) 应失败", bad)
		}
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
	r1, err := s.CreateRule(models.Rule{Name: "黑中介", Type: models.RuleTypeBlacklist, Value: "中介", Enabled: true, Priority: 10})
	if err != nil {
		t.Fatalf("建规则1: %v", err)
	}
	_, err = s.CreateRule(models.Rule{Name: "地铁近", Type: models.RuleTypeWhitelist, Value: "地铁", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatalf("建规则2: %v", err)
	}
	r3, err := s.CreateRule(models.Rule{Name: "停用规则", Type: models.RuleTypeBlacklist, Value: "x", Enabled: false})
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
	if err := s.UpdateRule(models.Rule{ID: r1.ID, Name: "黑中介", Type: models.RuleTypeBlacklist, Value: "中介,代理", Enabled: true, Priority: 10}); err != nil {
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

// EnsureDefaultRule：启用为 0 时种子黑白名单；已有启用则不重复
func TestEnsureDefaultRule(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	if err := s.EnsureDefaultRule(); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListRules(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("启用 = %d, want 3", len(rules))
	}
	byName := map[string]models.Rule{}
	for _, r := range rules {
		byName[r.Name] = r
	}
	if byName["黑名单-中介"].Value != "中介,代理,隔断," || byName["白名单-地点"].Value != "梨园,雍和宫" {
		t.Errorf("默认规则值不符: %+v", rules)
	}
	if byName["靠谱个人房源"].Type != models.RuleTypeAINatural || byName["靠谱个人房源"].Value != models.BuiltInAIRuleValue {
		t.Errorf("默认 AI 规则不符: %+v", byName["靠谱个人房源"])
	}
	if err := s.EnsureDefaultRule(); err != nil {
		t.Fatal(err)
	}
	rules, _ = s.ListRules(false)
	if len(rules) != 3 {
		t.Errorf("不应重复种子: %d", len(rules))
	}
}

// 禁止删除/禁用导致启用总数为 0；仅禁用的可删
func TestRulesLastEnabledGuard(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	r, err := s.CreateRule(models.Rule{Name: "唯一", Type: models.RuleTypeWhitelist, Value: "a", Enabled: true, Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(r.ID); !errors.Is(err, ErrLastEnabledRule) {
		t.Fatalf("删唯一启用: %v, want ErrLastEnabledRule", err)
	}
	if err := s.UpdateRule(models.Rule{ID: r.ID, Name: "唯一", Type: r.Type, Mode: r.Mode, Value: "a", Enabled: false, Priority: 1}); !errors.Is(err, ErrLastEnabledRule) {
		t.Fatalf("禁用唯一启用: %v, want ErrLastEnabledRule", err)
	}
	off, err := s.CreateRule(models.Rule{Name: "停用", Type: models.RuleTypeBlacklist, Value: "x", Enabled: false, Priority: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(off.ID); err != nil {
		t.Fatalf("删停用应成功: %v", err)
	}
	extra, err := s.CreateRule(models.Rule{Name: "另一条", Type: models.RuleTypeBlacklist, Value: "y", Enabled: true, Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(r.ID); err != nil {
		t.Fatalf("有另一启用时删除应成功: %v", err)
	}
	if err := s.UpdateRule(models.Rule{ID: extra.ID, Name: "另一条", Type: extra.Type, Mode: extra.Mode, Value: "y", Enabled: false, Priority: 2}); !errors.Is(err, ErrLastEnabledRule) {
		t.Fatalf("禁用最后启用: %v, want ErrLastEnabledRule", err)
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

	if err := s.AddUserFeedback(postID, models.FeedbackUseless, "价格虚假"); err != nil {
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

// system 标签写回后 AttachPostTags 能读到
func TestPostTagsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id := seedPost(t, s)
	if err := s.ReplaceSystemTags(id, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
		{Kind: models.TagKindLocation, Text: "14号线", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	list := []models.RentPost{{ID: id}}
	if err := s.AttachPostTags(list); err != nil {
		t.Fatal(err)
	}
	if len(list[0].Tags) != 2 || list[0].Tags[0].Text != "望京" {
		t.Errorf("Tags = %+v, want 望京+14号线", list[0].Tags)
	}
}

// 旧库仅有 posts 表时 Open 也会幂等补建 post_tags
func TestOpenCreatesPostTags(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
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

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 旧库失败: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='post_tags'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("应有 post_tags 表: n=%d err=%v", n, err)
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
		HardRules: []models.RuleHit{{RuleID: 3, Reason: "中介"}},
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

	r3 := models.FilterResult{PostID: postID, Status: models.PostStatusPassed,
		Stage: models.StageHardRule, DecidedAt: time.Now(),
		HardRules: []models.RuleHit{{RuleID: 1, Reason: "望京"}}}
	if err := s.SaveFilterResult(r3); err != nil {
		t.Fatalf("硬筛再写: %v", err)
	}
	got, ok, err = s.FilterResultByPostID(postID)
	if err != nil || !ok {
		t.Fatalf("硬筛后再读: ok=%v err=%v", ok, err)
	}
	if got.AI == nil || got.AI.Price != 4500 {
		t.Errorf("硬筛重放不应清掉 AI: %+v", got.AI)
	}
	if got.Stage != models.StageHardRule || len(got.HardRules) != 1 {
		t.Errorf("硬筛字段应更新: %+v", got)
	}
}

// ReplaceSystemTags 白名单地点入库
func TestReplaceSystemTags(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	postID := seedPost(t, s)
	if err := s.ReplaceSystemTags(postID, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
		{Kind: models.TagKindLocation, Text: "14号线", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	list := []models.RentPost{{ID: postID}}
	if err := s.AttachPostTags(list); err != nil {
		t.Fatal(err)
	}
	if len(list[0].Tags) != 2 || list[0].Tags[0].Text != "望京" {
		t.Errorf("标签未写回: %+v", list[0].Tags)
	}
}

// 筛选项不含人工备注、纯标点、过长句子
func TestListFilterTagsIgnoresManualAndJunk(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id := seedPost(t, s)
	if err := s.ReplaceSystemTags(id, []models.PostTag{
		{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem},
		{Kind: models.TagKindBlock, Text: ",,,", Source: models.TagSourceSystem},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUserFeedback(id, models.FeedbackUseless, "中介不能作为黑名单，因为原帖中通常都是“非中介”"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, datetime('now'))`,
		id, models.TagKindManual, "价格虚假", models.TagSourceUser); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListFilterTags()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"望京": true, "无用": true}
	if len(got) != 2 {
		t.Fatalf("筛选项 = %v, want 望京+无用", got)
	}
	for _, ft := range got {
		if !want[ft.Text] {
			t.Errorf("多出来的筛选项 %q", ft.Text)
		}
		if ft.Count < 1 {
			t.Errorf("%s count = %d, want >= 1", ft.Text, ft.Count)
		}
	}
}

// 筛选项按出现帖数降序
func TestListFilterTagsOrderByCount(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id1 := seedPost(t, s)
	id2 := seedPost(t, s)
	id3 := seedPost(t, s)
	if err := s.ReplaceSystemTags(id1, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(id2, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(id3, []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListFilterTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "望京" || got[0].Count != 2 || got[1].Text != "回龙观" || got[1].Count != 1 {
		t.Fatalf("筛选项 = %+v, want 望京×2 再 回龙观×1", got)
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
			HardRules: []models.RuleHit{{RuleID: 1, Reason: "望京"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddUserFeedback(p1, models.FeedbackUseless, "假房源"); err != nil {
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
		HardRules: []models.RuleHit{{RuleID: 1, Reason: "望京"}},
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

// 帖子列表：状态过滤 / 分页 / 空 status 全量（id 倒序）+ q/tag/handled（规格 §6）
func TestListPosts(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	statuses := []string{models.PostStatusCollected, models.PostStatusPassed, models.PostStatusRejected}
	for i, st := range statuses {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("lp%d", i), Title: "t",
			CollectedAt: time.Now(), Status: st}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListPosts(PostListFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("全量 = %d, want 3", len(all))
	}
	if all[0].Status != models.PostStatusRejected {
		t.Errorf("倒序错误: 首帖状态 = %s, want rejected（时间/id 最大）", all[0].Status)
	}

	passed, err := s.ListPosts(PostListFilter{Status: models.PostStatusPassed}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(passed) != 1 || passed[0].Status != models.PostStatusPassed {
		t.Errorf("过滤 passed = %+v, want 仅 1 帖", passed)
	}

	page, err := s.ListPosts(PostListFilter{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != models.PostStatusPassed {
		t.Errorf("分页 = %+v, want 1 帖 passed", page)
	}

	if _, err := s.InsertPost(models.RentPost{Source: "weibo", ExternalID: "wb1", Title: "微博帖",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}); err != nil {
		t.Fatal(err)
	}
	wb, err := s.ListPosts(PostListFilter{Source: "weibo"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(wb) != 1 || wb[0].ExternalID != "wb1" {
		t.Errorf("source=weibo = %+v", wb)
	}
}

// ListPosts 按发布时间倒序：id 更大但发布时间更早的帖应排在后面（回归 datetime() 解不了 Go 时间格式）
func TestListPostsOrderByPublishedAt(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	olderPub := time.Date(2026, 8, 6, 0, 36, 0, 0, time.Local)
	newerPub := time.Date(2026, 8, 11, 22, 35, 0, 0, time.Local)
	collected := time.Date(2026, 8, 15, 20, 0, 0, 0, time.Local)

	// 先插发布时间更新、id 会更小
	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "ord-early-id", Title: "晚发",
		PublishedAt: newerPub, CollectedAt: collected, Status: models.PostStatusPassed,
	}); err != nil {
		t.Fatal(err)
	}
	// 后插发布时间更早、id 会更大
	if _, err := s.InsertPost(models.RentPost{
		Source: "douban", ExternalID: "ord-late-id", Title: "早发",
		PublishedAt: olderPub, CollectedAt: collected, Status: models.PostStatusPassed,
	}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListPosts(PostListFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].ExternalID != "ord-early-id" || list[1].ExternalID != "ord-late-id" {
		t.Fatalf("顺序 = [%s, %s], want [ord-early-id, ord-late-id]（按发布时间，不是 id）",
			list[0].ExternalID, list[1].ExternalID)
	}
	if list[0].ID > list[1].ID {
		t.Fatalf("回归场景：首帖 id=%d > 次帖 id=%d，说明仍在按 id 排", list[0].ID, list[1].ID)
	}
}

// ListPosts 扩展筛选：q、tag（post_tags）、handled
func TestListPostsFilters(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	p1 := models.RentPost{Source: "douban", ExternalID: "f1", Title: "望京合租", Content: "近地铁",
		CollectedAt: time.Now(), Status: models.PostStatusPassed}
	p2 := models.RentPost{Source: "douban", ExternalID: "f2", Title: "回龙观次卧", Content: "望京通勤也可",
		CollectedAt: time.Now(), Status: models.PostStatusPassed}
	p3 := models.RentPost{Source: "douban", ExternalID: "f3", Title: "其它", Content: "无标签",
		CollectedAt: time.Now(), Status: models.PostStatusRejected}
	for _, p := range []models.RentPost{p1, p2, p3} {
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	var id1, id2 int64
	all, err := s.ListPosts(PostListFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		switch p.ExternalID {
		case "f1":
			id1 = p.ID
		case "f2":
			id2 = p.ID
		}
	}
	if err := s.ReplaceSystemTags(id1, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSystemTags(id2, []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPostHandled(id2); err != nil {
		t.Fatal(err)
	}

	byQ, err := s.ListPosts(PostListFilter{Q: "合租"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byQ) != 1 || byQ[0].ExternalID != "f1" {
		t.Errorf("q=合租 = %+v, want f1", byQ)
	}
	byContent, err := s.ListPosts(PostListFilter{Q: "望京通勤"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byContent) != 1 || byContent[0].ExternalID != "f2" {
		t.Errorf("q=望京通勤 = %+v, want f2", byContent)
	}

	byTag, err := s.ListPosts(PostListFilter{Tag: "望京"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag) != 1 || byTag[0].ExternalID != "f1" {
		t.Errorf("tag=望京 = %+v, want f1（不误伤 content）", byTag)
	}

	handled, err := s.ListPosts(PostListFilter{Handled: "1"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 1 || handled[0].ExternalID != "f2" || handled[0].HandledAt == nil {
		t.Errorf("handled=1 = %+v, want f2 且 HandledAt 非空", handled)
	}
	unhandled, err := s.ListPosts(PostListFilter{Handled: "0"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unhandled) != 2 {
		t.Errorf("handled=0 = %d, want 2", len(unhandled))
	}
}

// Mark/Clear handled_at：写清往返，不改 status
func TestMarkClearPostHandled(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	id := seedPost(t, s)

	if err := s.MarkPostHandled(id); err != nil {
		t.Fatal(err)
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("标记后 HandledAt 应非空: ok=%v err=%v p=%+v", ok, err, p)
	}
	if p.Status != models.PostStatusPassed {
		t.Errorf("status 被改成 %s, want passed", p.Status)
	}

	if err := s.ClearPostHandled(id); err != nil {
		t.Fatal(err)
	}
	p, ok, err = s.GetPost(id)
	if err != nil || !ok || p.HandledAt != nil {
		t.Fatalf("清除后 HandledAt 应 nil: ok=%v err=%v HandledAt=%v", ok, err, p.HandledAt)
	}
}

// 旧库无 handled_at：Open 幂等 ALTER 补列
func TestMigrateAddsHandledAtColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-handled.db")
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
	    address_tags TEXT NOT NULL DEFAULT '[]',
	    raw TEXT NOT NULL DEFAULT '', UNIQUE(source, external_id)
	)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 旧库失败: %v", err)
	}
	defer s.Close()
	ok, err := s.columnExists("posts", "handled_at")
	if err != nil || !ok {
		t.Fatalf("旧库补 handled_at 失败: ok=%v err=%v", ok, err)
	}
	ok, err = s.columnExists("posts", "price")
	if err != nil || !ok {
		t.Fatalf("旧库补 price 失败: ok=%v err=%v", ok, err)
	}
	ok, err = s.columnExists("posts", "contact")
	if err != nil || !ok {
		t.Fatalf("旧库补 contact 失败: ok=%v err=%v", ok, err)
	}
}

// 旧四 type+mode 经 Open/migrate 写成三 type（Spec 09 §2.2）
func TestMigrateRuleTypes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-rules.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE rules (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    name TEXT NOT NULL, type TEXT NOT NULL, mode TEXT NOT NULL,
	    value TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
	    priority INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	now := "2026-01-01 00:00:00"
	seeds := []struct {
		name, typ, mode, value string
	}{
		{"白", "hard_whitelist", "include", "望京"},
		{"黑", "hard_blacklist", "exclude", "中介"},
		{"关入", "hard_keyword", "include", "整租"},
		{"关出", "hard_keyword", "exclude", "合租"},
		{"关空", "hard_keyword", "", "中介"},
		{"AI", "ai_natural", "", "只要地铁近"},
		{"已新", "whitelist", "", "和平里"},
	}
	for _, s := range seeds {
		if _, err := legacy.Exec(`INSERT INTO rules (name,type,mode,value,enabled,priority,created_at) VALUES (?,?,?,?,1,1,?)`,
			s.name, s.typ, s.mode, s.value, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := legacy.Exec(`CREATE TABLE kv_config (
	    key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO kv_config (key,value,updated_at) VALUES ('rules.defaults_version','2',0)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	rules, err := st.ListRules(false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"白": "whitelist", "黑": "blacklist", "关入": "whitelist",
		"关出": "blacklist", "关空": "blacklist", "AI": "ai_natural", "已新": "whitelist",
	}
	if len(rules) != len(want) {
		t.Fatalf("规则数 = %d, want %d", len(rules), len(want))
	}
	for _, r := range rules {
		if got := want[r.Name]; got == "" || r.Type != got {
			t.Errorf("%s type = %q, want %q", r.Name, r.Type, got)
		}
	}
	// 再 Open 一次仍幂等
	st.Close()
	st2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	rules2, _ := st2.ListRules(false)
	if len(rules2) != len(want) {
		t.Errorf("幂等后规则数 = %d", len(rules2))
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
	if err := s.AddUserFeedback(id, models.FeedbackUseless, "假房源"); err != nil {
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

	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Kind != models.TagKindFeedback || tags[0].Text != "无用" {
		t.Errorf("帖子标签 = %+v, want 仅 feedback 无用（备注不进标签）", tags)
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

func TestListPostsByIDs(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	for i, title := range []string{"甲", "乙", "丙"} {
		p := models.RentPost{
			Source: "douban", ExternalID: fmt.Sprintf("by-id-%d", i), Title: title,
			CollectedAt: time.Now(), Status: models.PostStatusCollected,
		}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := s.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil || len(batch) != 3 {
		t.Fatalf("播种: n=%d err=%v", len(batch), err)
	}
	idA, idC := batch[0].ID, batch[2].ID
	got, err := s.ListPostsByIDs([]int64{idC, idA, idC, 99999, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != idC || got[1].ID != idA {
		t.Fatalf("got ids=%v, want [%d %d]", idsOf(got), idC, idA)
	}
}

func idsOf(posts []models.RentPost) []int64 {
	out := make([]int64, len(posts))
	for i, p := range posts {
		out[i] = p.ID
	}
	return out
}
