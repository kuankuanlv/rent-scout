package collector

import (
	"context"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// fakeSource 内存源：固定条目 + 详情，记录 Detail 调用次数
type fakeSource struct {
	name        string
	pages       [][]ListItem // 每页条目（模拟时间倒序）
	next        string       // List 固定返回的下一页游标（"" = 单页/末尾）
	details     map[string]models.RentPost
	detailCalls int
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) List(ctx context.Context, cursor string) ([]ListItem, string, error) {
	idx := 0
	if cursor == "1" {
		idx = 1
	}
	if idx >= len(f.pages) {
		return nil, f.next, nil
	}
	return f.pages[idx], f.next, nil
}
func (f *fakeSource) Detail(ctx context.Context, item ListItem) (models.RentPost, error) {
	f.detailCalls++
	return f.details[item.ExternalID], nil
}

func testRunner(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	rt := config.NewRuntime(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 3600, JitterRatio: 0.2, MaxAgeDays: 7,
			Sources: []string{"fake"},
			Douban:  config.DoubanConfig{Groups: []string{"x"}},
		},
	})
	r := NewRunner(rt, &config.EnvLocalConfig{}, st, nil, nil)
	return r, st
}

// 一轮采集：列表页条目 → 时间窗内入库、超窗跳过、游标推进
func TestRunnerCollectsNewPosts(t *testing.T) {
	now := time.Now()
	src := &fakeSource{
		name: "fake",
		pages: [][]ListItem{{
			{ExternalID: "a", URL: "u/a", Title: "新帖", PublishedAt: now.Add(-2 * 24 * time.Hour)},
			{ExternalID: "b", URL: "u/b", Title: "老帖", PublishedAt: now.Add(-30 * 24 * time.Hour)},
		}},
		details: map[string]models.RentPost{
			"a": {Source: "fake", ExternalID: "a", Title: "新帖", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}
	trigger := make(chan struct{}, 10)
	r, st := testRunner(t)
	defer st.Close()

	if err := r.runSourceOnce(context.Background(), src, trigger); err != nil {
		t.Fatal(err)
	}
	// 入库 1 条（超窗的 b 跳过）；trigger 收到 1 个信号
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || batch[0].ExternalID != "a" {
		t.Errorf("入库 = %+v, want 仅 a", batch)
	}
	if src.detailCalls != 1 {
		t.Errorf("Detail 调用 = %d, want 1（老帖不抓详情）", src.detailCalls)
	}
	select {
	case <-trigger:
	default:
		t.Error("应发送 trigger 信号")
	}
	// 游标已保存
	if _, ok, err := st.GetCursor("fake"); err != nil || !ok {
		t.Errorf("游标未保存: ok=%v err=%v", ok, err)
	}
}

// 二次运行：已存在的帖子不再抓详情页（列表页先行去重，调整规格 E）
func TestRunnerSkipsExisting(t *testing.T) {
	now := time.Now()
	src := &fakeSource{
		name:  "fake",
		pages: [][]ListItem{{{ExternalID: "a", URL: "u/a", Title: "t", PublishedAt: now}}},
		details: map[string]models.RentPost{
			"a": {Source: "fake", ExternalID: "a", Title: "t", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}
	r, st := testRunner(t)
	defer st.Close()

	if err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	// 第二轮：a 已在库 → 不再调 Detail
	if err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 1 {
		t.Errorf("Detail 调用 = %d, want 1（第二轮应跳过已存在）", src.detailCalls)
	}
}

// 空页游标推进：空组（items 空 + next="1:0"）→ 游标保存为 "1:0"，不调 Detail，
// 避免多组配置下每轮卡死在空组（P2-9 审查发现）
func TestRunnerAdvancesPastEmptyPage(t *testing.T) {
	src := &fakeSource{
		name:  "fake",
		pages: [][]ListItem{{}, {{ExternalID: "a", URL: "u/a", Title: "t", PublishedAt: time.Now()}}},
		next:  "1:0", // 空页指向下一组
		details: map[string]models.RentPost{
			"a": {Source: "fake", ExternalID: "a", Title: "t", CollectedAt: time.Now(), Status: models.PostStatusCollected},
		},
	}
	r, st := testRunner(t)
	defer st.Close()

	if err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	// 游标推进到 "1:0"（跟随源协议）
	cursor, ok, err := st.GetCursor("fake")
	if err != nil || !ok {
		t.Fatalf("游标未保存: ok=%v err=%v", ok, err)
	}
	if cursor != "1:0" {
		t.Errorf("游标 = %q, want 1:0", cursor)
	}
	// 空页无新帖 → 不调 Detail
	if src.detailCalls != 0 {
		t.Errorf("Detail 调用 = %d, want 0", src.detailCalls)
	}
}
