package notifier

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// fetchReady 构造支持 fetch 的环境：AI 关闭（无 AI 结果也可通知）+ feishu 渠道
func fetchReady(t *testing.T, batchSize, posts int) (*NotifierService, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for i := 0; i < posts; i++ {
		p := models.RentPost{
			Source:      "test",
			ExternalID:  "id-" + string(rune('0'+i)),
			Title:       "t" + string(rune('0'+i)),
			Status:      models.PostStatusPassed,
			CollectedAt: time.Now(),
		}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	app := config.DefaultApp()
	noAI := false
	app.Filter.AIEnabled = &noAI
	app.Notifier.BatchSize = batchSize
	app.Notifier.Channels = []string{"feishu"}
	rt := config.NewHotConfigWithSnapshot(app, &config.Secrets{
		Notifier: config.SecretsNotifier{
			Feishu: config.WebhookSecretConfig{Webhook: "https://example.com/hook"},
		},
	})
	svc, err := New(Options{Config: rt, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	return svc, s
}

// 凑批语义：fetch 的 limit 来自 batch 的 BatchSize，不得被热配置二次覆盖截短。
// 配置 BatchSize=5，预置 3 条 passed 帖，fetch(ctx, 20) 应返回全部 3 条。
func TestFetchReturnsAllWhenConfigBatchSmaller(t *testing.T) {
	svc, _ := fetchReady(t, 5, 3)
	posts, err := svc.fetch(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 3 {
		t.Fatalf("fetch 被 Notifier.BatchSize=5 截短或遗漏：len=%d want 3", len(posts))
	}
}

// 回归防线：库内积压超过配置 BatchSize 时不受限制，抽批上限只由 batch 的 limit 决定。
// 旧行为把 fetch limit 覆盖成 5，这里应返回全部 10 条。
func TestFetchNotTruncatedByConfigBatchSize(t *testing.T) {
	svc, _ := fetchReady(t, 5, 10)
	posts, err := svc.fetch(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 10 {
		t.Fatalf("fetch 被 Notifier.BatchSize=5 截短：len=%d want 10", len(posts))
	}
}

// 一致性：batch 会用配置的 BatchSize 作为 limit（=10），fetch 返回不超过该上限。
func TestFetchRespectsPipelineLimit(t *testing.T) {
	svc, _ := fetchReady(t, 10, 20)
	posts, err := svc.fetch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 10 {
		t.Fatalf("fetch 应按 batch limit=10 返回：len=%d want 10", len(posts))
	}
}
