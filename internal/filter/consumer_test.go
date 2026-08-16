package filter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

func TestConsumerProcessHard(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeWhitelist,
		Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	p1 := models.RentPost{Source: "douban", ExternalID: "a", Title: "望京整租", Content: "近望京地铁",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	p2 := models.RentPost{Source: "douban", ExternalID: "b", Title: "回龙观", Content: "两居",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	st.InsertPost(p1)
	st.InsertPost(p2)

	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.processHard(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
	rejected, _ := st.FetchPendingByStatus(models.PostStatusRejected, 10)
	if len(passed) != 1 || len(rejected) != 1 {
		t.Fatalf("passed=%d rejected=%d, want 1/1", len(passed), len(rejected))
	}
	if len(passed[0].AddressTags) != 1 || passed[0].AddressTags[0] != "望京" {
		t.Errorf("通过帖标签 = %v, want [望京]", passed[0].AddressTags)
	}
	fr, ok, _ := st.FilterResultByPostID(rejected[0].ID)
	if !ok || fr.RejectedBy != "默认拒绝" {
		t.Errorf("未命中拒绝应写默认拒绝: ok=%v %+v", ok, fr)
	}
	if err := st.AttachHitTags(rejected); err != nil {
		t.Fatal(err)
	}
	if len(rejected[0].HitTags) != 1 || rejected[0].HitTags[0].Text != "默认拒绝" {
		t.Errorf("展示标签 = %+v, want [默认拒绝]", rejected[0].HitTags)
	}
}

func TestConsumerRejects(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	defer st.Close()
	if _, err := st.CreateRule(models.Rule{Name: "中介", Type: models.RuleTypeBlacklist,
		Value: "中介", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	p := models.RentPost{Source: "douban", ExternalID: "c", Title: "中介勿扰", Content: "中介勿扰，代理绕行",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	st.InsertPost(p)
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	batch, _ := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err := c.processHard(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	res, ok, err := st.FilterResultByPostID(1)
	if err != nil || !ok {
		t.Fatalf("结果缺失: ok=%v err=%v", ok, err)
	}
	if res.Status != models.PostStatusRejected || !contains(res.RejectedBy, "中介") {
		t.Errorf("拒绝记录错误: %+v", res)
	}
}

type fakeAIEvaluator struct {
	calls    int
	batches  [][]models.RentPost
	err      error
	failOnce bool
	results  map[int64]*models.AIResult
	gotPosts []models.RentPost
	gotRules []models.Rule
}

func (f *fakeAIEvaluator) EvaluateBatch(ctx context.Context, posts []models.RentPost, aiRules []models.Rule) (map[int64]*models.AIResult, error) {
	f.calls++
	f.batches = append(f.batches, append([]models.RentPost(nil), posts...))
	f.gotPosts = append([]models.RentPost(nil), posts...)
	f.gotRules = append([]models.Rule(nil), aiRules...)
	if f.err != nil {
		return nil, f.err
	}
	if f.failOnce && f.calls == 1 {
		return nil, context.DeadlineExceeded
	}
	out := make(map[int64]*models.AIResult, len(posts))
	for _, p := range posts {
		out[p.ID] = f.results[p.ID]
	}
	return out, nil
}

func setupAIConsumer(t *testing.T, ai AIEvaluator, opts ConsumerOptions) (*Consumer, *store.Store, []models.RentPost) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateRule(models.Rule{Name: "地点", Type: models.RuleTypeWhitelist,
		Value: "回龙观", Enabled: true, Priority: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRule(models.Rule{Name: "AI筛选", Type: models.RuleTypeAINatural,
		Value: "只要地铁 1 公里内的整租", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	for _, eid := range []string{"a1", "a2", "a3"} {
		if _, err := st.InsertPost(models.RentPost{Source: "douban", ExternalID: eid,
			Title: "回龙观两居", Content: "五环外普通两居", CollectedAt: time.Now(), Status: models.PostStatusCollected}); err != nil {
			t.Fatal(err)
		}
	}
	c := NewConsumerWithOptions(NewRuleChain(ai), st, opts)
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	return c, st, batch
}

func TestConsumerAIBatch(t *testing.T) {
	t.Run("只审已通过帖", func(t *testing.T) {
		fake := &fakeAIEvaluator{}
		c, st, batch := setupAIConsumer(t, fake, ConsumerOptions{})
		fake.results = map[int64]*models.AIResult{
			batch[0].ID: {Passed: true, Reason: "位置好", Price: 4500, Contact: "wx_ok"},
			batch[1].ID: {Passed: false, Reason: "超出预算", Price: 9000},
			batch[2].ID: {Passed: true, Reason: "通勤方便", Price: 4200},
		}
		if err := c.processHard(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 0 {
			t.Errorf("硬规则阶段不应调 AI, calls=%d", fake.calls)
		}
		awaiting, err := c.FetchAwaitingAI(context.Background(), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(awaiting) != 3 {
			t.Fatalf("待 AI = %d, want 3", len(awaiting))
		}
		if err := c.processAI(context.Background(), awaiting); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 1 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 1", fake.calls)
		}
		p0, ok, err := st.GetPost(batch[0].ID)
		if err != nil || !ok || p0.Price != "4500" || p0.Contact != "wx_ok" {
			t.Errorf("AI 价格/联系方式未写库: ok=%v price=%q contact=%q err=%v", ok, p0.Price, p0.Contact, err)
		}
		passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
		rejected, _ := st.FetchPendingByStatus(models.PostStatusRejected, 10)
		if len(passed) != 3 || len(rejected) != 0 {
			t.Errorf("passed=%d rejected=%d, want 3/0（AI 不改主状态）", len(passed), len(rejected))
		}
	})

	t.Run("失败保持 passed 不写 ai_result", func(t *testing.T) {
		fake := &fakeAIEvaluator{err: context.DeadlineExceeded}
		c, st, batch := setupAIConsumer(t, fake, ConsumerOptions{})
		if err := c.processHard(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		awaiting, _ := c.FetchAwaitingAI(context.Background(), 10)
		if err := c.processAI(context.Background(), awaiting); err != nil {
			t.Fatal(err)
		}
		passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
		if len(passed) != 3 {
			t.Errorf("passed=%d, want 3", len(passed))
		}
		still, _ := c.FetchAwaitingAI(context.Background(), 10)
		if len(still) != 3 {
			t.Errorf("仍待 AI = %d, want 3", len(still))
		}
	})
}

func TestConsumerAISubBatchSplit(t *testing.T) {
	fake := &fakeAIEvaluator{}
	c, _, batch := setupAIConsumer(t, fake, ConsumerOptions{AIBatchSize: 2})
	fake.results = map[int64]*models.AIResult{}
	for _, p := range batch {
		fake.results[p.ID] = &models.AIResult{Passed: true, Reason: "ok", Price: 4000}
	}
	if err := c.processHard(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	awaiting, _ := c.FetchAwaitingAI(context.Background(), 10)
	if err := c.processAI(context.Background(), awaiting); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 {
		t.Errorf("EvaluateBatch 调用次数 = %d, want 2", fake.calls)
	}
}

func TestFetchAwaitingAIEmptyWhenOff(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeWhitelist,
		Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	st.InsertPost(models.RentPost{Source: "douban", ExternalID: "a", Title: "望京整租", Content: "x",
		CollectedAt: time.Now(), Status: models.PostStatusCollected})
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	batch, _ := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	_ = c.processHard(context.Background(), batch)
	got, err := c.FetchAwaitingAI(context.Background(), 10)
	if err != nil || len(got) != 0 {
		t.Errorf("AI 关闭应空捞: n=%d err=%v", len(got), err)
	}
}

func TestConsumerRulesReadErrorKeepsBatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rules-fail.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeWhitelist,
		Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	for _, eid := range []string{"r1", "r2"} {
		if _, err := st.InsertPost(models.RentPost{Source: "douban", ExternalID: eid,
			Title: "望京两居", Content: "x", CollectedAt: time.Now(), Status: models.PostStatusCollected}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	st.Close()
	if err := c.processHard(context.Background(), batch); err != nil {
		t.Fatalf("processHard 应只记录告警不返回 error: %v", err)
	}
	verify, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close()
	still, err := verify.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(still) != 2 {
		t.Errorf("collected 数 = %d, want 2", len(still))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsIdx(s, sub))
}
func containsIdx(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestReplayHardRejectedToPassedClearsNotifications(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := models.RentPost{Source: "douban", ExternalID: "old-rej", Title: "望京整租", Content: "近望京",
		PublishedAt: time.Now(), CollectedAt: time.Now(), Status: models.PostStatusRejected}
	if _, err := st.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	batch, err := st.ListPublishedBetween(time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
	if err != nil || len(batch) != 1 {
		t.Fatalf("拉帖: n=%d err=%v", len(batch), err)
	}
	if _, err := st.InsertNotification(batch[0].ID, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNotificationSent(batch[0].ID, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeWhitelist,
		Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	if err := c.ReplayHard(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	got, err := st.FetchPendingByStatus(models.PostStatusPassed, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("应变成 passed: n=%d err=%v", len(got), err)
	}
	notes, err := st.ListNotificationsByPost(got[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("拒绝变通过应清账本以便重推: %+v", notes)
	}
}

func TestReplayHardStillPassedKeepsNotifications(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeWhitelist,
		Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	p := models.RentPost{Source: "douban", ExternalID: "keep-sent", Title: "望京整租", Content: "近望京",
		PublishedAt: time.Now(), CollectedAt: time.Now(), Status: models.PostStatusPassed}
	if _, err := st.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	batch, err := st.ListPublishedBetween(time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
	if err != nil || len(batch) != 1 {
		t.Fatalf("拉帖: n=%d err=%v", len(batch), err)
	}
	if _, err := st.InsertNotification(batch[0].ID, "pushplus"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNotificationSent(batch[0].ID, "pushplus"); err != nil {
		t.Fatal(err)
	}
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{})
	if err := c.ReplayHard(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	notes, err := st.ListNotificationsByPost(batch[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Status != models.NotifyStatusSent {
		t.Errorf("仍通过不应重推: %+v", notes)
	}
}

func TestFetchAwaitingAISkipsWhenDisabled(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	off := false
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{Filter: config.FilterConfig{AIEnabled: &off}}, nil)
	c := NewConsumerWithOptions(NewRuleChain(nil), st, ConsumerOptions{HotConfig: rt})
	got, err := c.FetchAwaitingAI(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("未启用应空捞, got %d", len(got))
	}
}
