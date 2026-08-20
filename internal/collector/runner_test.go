package collector

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

type fakeSource struct {
	name        string
	pages       [][]ListItem
	details     map[string]models.RentPost
	detailCalls int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) NewIterator(state string, start, end time.Time) Iterator {
	return &fakeIterator{
		cursor: strings.TrimSpace(state),
		start:  start,
		end:    end,
		listFn: f.List,
	}
}

func (f *fakeSource) List(ctx context.Context, cursor string) ([]ListItem, string, error) {
	idx := fakePageIndex(cursor)
	if idx >= len(f.pages) {
		return nil, "", nil
	}
	next := ""
	if idx+1 < len(f.pages) {
		next = strconv.Itoa(idx+1) + ":0"
	}
	return f.pages[idx], next, nil
}

func (f *fakeSource) Detail(ctx context.Context, item ListItem) (models.RentPost, error) {
	f.detailCalls++
	return f.details[item.ExternalID], nil
}

type fakeIterator struct {
	cursor  string
	start   time.Time
	end     time.Time
	current []ListItem
	err     error
	listFn  func(context.Context, string) ([]ListItem, string, error)
}

func (it *fakeIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}
	items, next, err := it.listFn(ctx, it.cursor)
	if err != nil {
		it.err = err
		return false
	}
	var filtered []ListItem
	for _, item := range items {
		if !it.start.IsZero() && item.PublishedAt.Before(it.start) {
			continue
		}
		if !it.end.IsZero() && item.PublishedAt.After(it.end) {
			continue
		}
		filtered = append(filtered, item)
	}
	it.current = filtered
	it.cursor = next
	return next != "" || len(items) > 0
}

func (it *fakeIterator) Value() []ListItem { return it.current }
func (it *fakeIterator) Checkpoint() string { return it.cursor }
func (it *fakeIterator) Err() error         { return it.err }

func fakePageIndex(cursor string) int {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(cursor, ":", 2)[0])
	return n
}

func testRunner(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 3600, JitterRatio: 0, MaxAgeDays: 7,
			Sources: []string{"fake"},
		},
	}, nil)
	return NewRunner(rt, st, nil, nil), st
}

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
	trigger := make(chan struct{}, 1)
	r, st := testRunner(t)
	defer st.Close()

	if _, err := r.runSourceOnce(context.Background(), src, trigger); err != nil {
		t.Fatal(err)
	}
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || batch[0].ExternalID != "a" {
		t.Fatalf("入库 = %+v, want 仅 a", batch)
	}
	if src.detailCalls != 1 {
		t.Errorf("Detail = %d, want 1", src.detailCalls)
	}
	select {
	case <-trigger:
	default:
		t.Error("应有 trigger 信号")
	}
}

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

	if _, err := r.runSourceOnce(context.Background(), src, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runSourceOnce(context.Background(), src, nil); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 1 {
		t.Errorf("Detail = %d, want 1（已存在不再抓）", src.detailCalls)
	}
}

func TestSetEnabled(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{Sources: []string{"fake"}},
	}, nil)
	src := &fakeSource{name: "fake", pages: [][]ListItem{{{ExternalID: "a", PublishedAt: time.Now()}}}}
	r := NewRunner(rt, st, []Source{src}, nil)

	if !r.SourceEnabled("fake") {
		t.Fatal("默认应启用")
	}
	if err := r.SetEnabled("unknown", false); err == nil {
		t.Fatal("未知源应报错")
	}
	if err := r.SetEnabled("fake", false); err != nil {
		t.Fatal(err)
	}
	if r.SourceEnabled("fake") {
		t.Fatal("disable 后应停用")
	}
	if err := r.SetEnabled("fake", true); err != nil {
		t.Fatal(err)
	}
	if !r.SourceEnabled("fake") {
		t.Fatal("enable 后应恢复")
	}
}

func TestSourceEnabledFollowsConfig(t *testing.T) {
	r, st := testRunner(t)
	defer st.Close()
	if !r.SourceEnabled("fake") {
		t.Fatal("配置含 fake 时应启用")
	}
	r.rt.Get().Collector.Sources = nil
	if r.SourceEnabled("fake") {
		t.Fatal("配置去掉源后应视为未启用")
	}
}
