package collector

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
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
	listCalls   atomic.Int32 // 循环测试用：每轮至少调一次 List（并发安全）
	cursors     []string
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) List(ctx context.Context, cursor string) ([]ListItem, string, error) {
	f.listCalls.Add(1)
	f.cursors = append(f.cursors, cursor)
	idx := fakePageIndex(cursor)
	if idx >= len(f.pages) {
		return nil, "", nil
	}
	next := f.next
	if next == "" && idx+1 < len(f.pages) {
		next = strconv.Itoa(idx+1) + ":0"
	}
	return f.pages[idx], next, nil
}
func (f *fakeSource) Detail(ctx context.Context, item ListItem) (models.RentPost, error) {
	f.detailCalls++
	return f.details[item.ExternalID], nil
}

type windowSpy struct {
	fakeSource
	winStart, winEnd time.Time
}

func (f *windowSpy) ListInWindow(ctx context.Context, cursor string, start, end time.Time) ([]ListItem, string, error) {
	f.winStart, f.winEnd = start, end
	return f.List(ctx, cursor)
}

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
			Interval: 3600, JitterRatio: 0.2, MaxAgeDays: 7,
			Sources: []string{"fake"},
			Douban:  config.DoubanConfig{Groups: []string{"x"}},
		},
	}, nil)
	r := NewRunner(rt, st, nil, nil)
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

	if _, err := r.runSourceOnce(context.Background(), src, trigger); err != nil {
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
	prog, ok, err := st.GetProgress("fake")
	if err != nil || !ok {
		t.Errorf("进度未保存: ok=%v err=%v", ok, err)
	}
	if !prog.CatchingUp() || prog.SeenNewest == "" {
		t.Errorf("进度 = %+v, want 已追新且带 seen_newest", prog)
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

	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	// 第二轮：a 已在库 → 不再调 Detail
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 1 {
		t.Errorf("Detail 调用 = %d, want 1（第二轮应跳过已存在）", src.detailCalls)
	}
}

// 空页不卡死：同一轮走进下一组并采集
func TestRunnerAdvancesPastEmptyPage(t *testing.T) {
	now := time.Now()
	src := &fakeSource{
		name:  "fake",
		pages: [][]ListItem{{}, {{ExternalID: "a", URL: "u/a", Title: "t", PublishedAt: now}}},
		details: map[string]models.RentPost{
			"a": {Source: "fake", ExternalID: "a", Title: "t", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}
	r, st := testRunner(t)
	defer st.Close()

	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 10)); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 1 {
		t.Errorf("空组后应在同一轮抓下一组, Detail=%d want 1", src.detailCalls)
	}
	prog, ok, err := st.GetProgress("fake")
	if err != nil || !ok {
		t.Fatalf("进度未保存: ok=%v err=%v", ok, err)
	}
	if !prog.CatchingUp() {
		t.Errorf("进度 = %+v, want 已追新", prog)
	}
}

// newControlRunner 控制接口测试用 runner：单 fake 源，1s 周期无抖动（循环测试可预期）
func newControlRunner(t *testing.T) (*Runner, *fakeSource) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 1, JitterRatio: 0, MaxAgeDays: 7,
			Sources: []string{"fake"},
			Douban:  config.DoubanConfig{Groups: []string{"x"}},
		},
	}, nil)
	src := &fakeSource{
		name:    "fake",
		pages:   [][]ListItem{{{ExternalID: "a", URL: "u/a", Title: "t", PublishedAt: time.Now()}}},
		details: map[string]models.RentPost{"a": {Source: "fake", ExternalID: "a", Title: "t", CollectedAt: time.Now(), Status: models.PostStatusCollected}},
	}
	r := NewRunner(rt, st, []Source{src}, nil)
	return r, src
}

// waitFor 轮询等待条件成立（≤timeout，10ms 间隔）；超时 t.Fatal
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestSetEnabled：默认全 true；未知源报错；disable 后 SourceEnabled=false
func TestSetEnabled(t *testing.T) {
	r, _ := newControlRunner(t)
	if !r.SourceEnabled("fake") {
		t.Error("默认源应为启用")
	}
	if err := r.SetEnabled("unknown", false); err == nil {
		t.Error("未知源 SetEnabled 应报错")
	}
	if err := r.SetEnabled("fake", false); err != nil {
		t.Fatal(err)
	}
	if r.SourceEnabled("fake") {
		t.Error("disable 后 SourceEnabled 应为 false")
	}
	if err := r.SetEnabled("fake", true); err != nil {
		t.Fatal(err)
	}
	if !r.SourceEnabled("fake") {
		t.Error("enable 后 SourceEnabled 应为 true")
	}
}

// TestTrigger：Trigger 后 runSource 至少执行一轮（fake Source 计数）；
// manual 通道容量 1 非阻塞——Trigger 不等待循环；未知源报错
func TestTrigger(t *testing.T) {
	r, src := newControlRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	if err := r.Trigger("fake"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return src.listCalls.Load() >= 1 }, "Trigger 后应至少执行一轮采集")
	if err := r.Trigger("unknown"); err == nil {
		t.Error("未知源 Trigger 应报错")
	}
}

// TestSourceEnabledLoop：停用态不执行轮次（fake Source 计数不变）；
// Trigger 后即使停用也执行一轮；重新启用后循环自然恢复（无需额外信号）
func TestSourceEnabledLoop(t *testing.T) {
	r, src := newControlRunner(t)
	if err := r.SetEnabled("fake", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	// 停用态：等待超过一个轮询周期，不执行轮次
	time.Sleep(1500 * time.Millisecond)
	if n := src.listCalls.Load(); n != 0 {
		t.Fatalf("停用态不应执行轮次, listCalls=%d", n)
	}

	// Trigger：即使停用也执行一轮（规格 7.1 手动触发抓取）
	if err := r.Trigger("fake"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return src.listCalls.Load() >= 1 }, "停用态 Trigger 后应执行一轮")

	// 重新启用：循环自然恢复（周期轮询后按 1s 周期自然跑轮）
	base := src.listCalls.Load()
	if err := r.SetEnabled("fake", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return src.listCalls.Load() >= base+1 }, "重新启用后应自然恢复轮次")
}

// 回填走完后进入 incremental：第二轮只打列表头，不再翻旧页
func TestRunnerIncrementalSkipsOldPages(t *testing.T) {
	now := time.Now()
	src := &fakeSource{
		name: "fake",
		pages: [][]ListItem{
			{{ExternalID: "new", URL: "u/new", Title: "新", PublishedAt: now.Add(-time.Hour)}},
			{{ExternalID: "old", URL: "u/old", Title: "旧", PublishedAt: now.Add(-2 * 24 * time.Hour)}},
		},
		details: map[string]models.RentPost{
			"new": {Source: "fake", ExternalID: "new", Title: "新", CollectedAt: now, Status: models.PostStatusCollected},
			"old": {Source: "fake", ExternalID: "old", Title: "旧", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}
	r, st := testRunner(t)
	defer st.Close()
	trig := make(chan struct{}, 10)
	if _, err := r.runSourceOnce(context.Background(), src, trig); err != nil {
		t.Fatal(err)
	}
	if src.listCalls.Load() != 2 {
		t.Fatalf("回填 List = %d, want 2 页", src.listCalls.Load())
	}
	prog, ok, err := st.GetProgress("fake")
	if err != nil || !ok || !prog.CatchingUp() {
		t.Fatalf("回填后进度 = %+v ok=%v err=%v, want 已追新", prog, ok, err)
	}

	src.cursors = nil
	if _, err := r.runSourceOnce(context.Background(), src, trig); err != nil {
		t.Fatal(err)
	}
	if src.listCalls.Load() != 3 {
		t.Errorf("增量后总 List = %d, want 3（第二轮只打首页）", src.listCalls.Load())
	}
	if len(src.cursors) != 1 || src.cursors[0] != "" {
		t.Errorf("增量 cursors = %v, want 仅空串（列表头）", src.cursors)
	}
	if src.detailCalls != 2 {
		t.Errorf("Detail = %d, want 2（增量不再抓旧帖）", src.detailCalls)
	}
}

// 翻历史进度停在搜索5，也不能证明搜索1～4本轮不用采；每轮仍从第1组列表头开始
func TestRunnerAlwaysStartsFromFirstGroup(t *testing.T) {
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
	if err := st.SetProgress("fake", store.SourceProgress{
		Page: "4:0", SeenNewest: now.Add(-24 * time.Hour).Format(time.RFC3339Nano),
		Fingerprint: sourceFingerprint("fake", r.rt.Get()),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 4)); err != nil {
		t.Fatal(err)
	}
	if len(src.cursors) == 0 || src.cursors[0] != "" {
		t.Errorf("本轮首个 cursor = %v, want 空串（搜索1第1页）", src.cursors)
	}
}

// 时间窗/源配置变了：旧 page 作废，从列表头重新回填
func TestRunnerResetsWhenRangeChanges(t *testing.T) {
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
	if err := st.SetProgress("fake", store.SourceProgress{
		Page: "1:0", Fingerprint: "old-range",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 4)); err != nil {
		t.Fatal(err)
	}
	if len(src.cursors) == 0 || src.cursors[0] != "" {
		t.Errorf("重置后首个 cursor = %v, want 空串", src.cursors)
	}
}

// 目标清单变了会换指纹并重置；同一清单只升级指纹格式，水位留下
func TestRunnerKeepsProgressWhenURLsAdded(t *testing.T) {
	now := time.Now()
	wm := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	src := &weiboWMSource{fakeSource: fakeSource{
		name: models.SourceWeibo.String(),
		pages: [][]ListItem{{{
			ExternalID: "old", URL: "u/old", Title: "旧",
			PublishedAt: now.Add(-3 * time.Hour),
		}}},
		details: map[string]models.RentPost{
			"old": {Source: "weibo", ExternalID: "old", Title: "旧", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}}
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 3600, MaxAgeDays: 7, Sources: []string{"weibo"},
			Weibo: config.WeiboConfig{
				RangeFrom: "-10",
				Users:     []string{"1234567890"},
			},
		},
	}, nil)
	r := NewRunner(rt, st, nil, nil)
	fp := sourceFingerprint("weibo", rt.Get())
	if err := st.SetProgress("weibo", store.SourceProgress{
		Page: "", SeenNewest: store.EncodeWatermarks(map[string]string{"user:1234567890": wm}), Fingerprint: fp,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 4)); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 0 {
		t.Errorf("同清单不应重翻旧帖, Detail=%d", src.detailCalls)
	}
	prog, ok, err := st.GetProgress("weibo")
	if err != nil || !ok {
		t.Fatalf("进度: ok=%v err=%v", ok, err)
	}
	if !prog.CatchingUp() {
		t.Errorf("进度 = %+v, want 已追新", prog)
	}
	if prog.Fingerprint != fp {
		t.Errorf("指纹 = %q, want %s", prog.Fingerprint, fp)
	}
}

type wmSource struct {
	fakeSource
}

type weiboWMSource struct {
	fakeSource
}

func (weiboWMSource) WatermarkKey(string) string { return "user:1234567890" }
func (weiboWMSource) TimeOrdered(string) bool    { return true }

func (f *wmSource) SkipGroup(cursor string) string {
	g, _ := parsePageCursor(cursor)
	if g >= len(f.pages) {
		return ""
	}
	return strconv.Itoa(g) + ":0"
}

func (f *wmSource) WatermarkKey(cursor string) string {
	g, _ := parsePageCursor(cursor)
	return "t:" + strconv.Itoa(g)
}

func (f *wmSource) TimeOrdered(cursor string) bool { return true }

func TestRunnerPerTargetWatermark(t *testing.T) {
	now := time.Now()
	src := &wmSource{fakeSource: fakeSource{
		name: "fake",
		pages: [][]ListItem{
			{{ExternalID: "hot", URL: "u/hot", Title: "热", PublishedAt: now.Add(-time.Minute)}},
			{{ExternalID: "slow", URL: "u/slow", Title: "慢", PublishedAt: now.Add(-3 * time.Hour)}},
		},
		details: map[string]models.RentPost{
			"hot":  {Source: "fake", ExternalID: "hot", Title: "热", CollectedAt: now, Status: models.PostStatusCollected},
			"slow": {Source: "fake", ExternalID: "slow", Title: "慢", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}}
	r, st := testRunner(t)
	defer st.Close()
	trig := make(chan struct{}, 4)
	if _, err := r.runSourceOnce(context.Background(), src, trig); err != nil {
		t.Fatal(err)
	}
	src.detailCalls = 0
	src.listCalls.Store(0)
	if _, err := r.runSourceOnce(context.Background(), src, trig); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 0 {
		t.Fatalf("第二轮已存在帖不应再 Detail=%d", src.detailCalls)
	}
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("两路都应入库, got %d", len(batch))
	}
}

type unorderedSrc struct {
	fakeSource
}

func (f *unorderedSrc) WatermarkKey(string) string  { return "" }
func (f *unorderedSrc) TimeOrdered(string) bool     { return false }
func (f *unorderedSrc) SkipGroup(string) string     { return "" }

func TestRunnerUnorderedSkipsWatermark(t *testing.T) {
	now := time.Now()
	old := now.Add(-5 * 24 * time.Hour)
	fresh := now.Add(-time.Hour)
	src := &unorderedSrc{fakeSource: fakeSource{
		name: "fake",
		pages: [][]ListItem{{
			{ExternalID: "new1", URL: "u/1", Title: "新1", PublishedAt: fresh},
			{ExternalID: "old", URL: "u/o", Title: "旧", PublishedAt: old},
			{ExternalID: "new2", URL: "u/2", Title: "新2", PublishedAt: fresh.Add(-time.Minute)},
		}},
		next: "",
		details: map[string]models.RentPost{
			"new1": {Source: "fake", ExternalID: "new1", Title: "新1", CollectedAt: now, Status: models.PostStatusCollected},
			"old":  {Source: "fake", ExternalID: "old", Title: "旧", CollectedAt: now, Status: models.PostStatusCollected},
			"new2": {Source: "fake", ExternalID: "new2", Title: "新2", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}}
	r, st := testRunner(t)
	defer st.Close()
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 2)); err != nil {
		t.Fatal(err)
	}
	if src.detailCalls != 3 {
		t.Fatalf("无序源不应被中间旧帖打断, Detail=%d want 3", src.detailCalls)
	}
}

// 追新水位只过滤帖，不改高级搜索 timescope；链接仍是 range_from→now
func TestRunnerListWindowKeepsRangeFrom(t *testing.T) {
	now := time.Now()
	src := &windowSpy{fakeSource: fakeSource{
		name:  models.SourceWeibo.String(),
		pages: [][]ListItem{{{ExternalID: "a", URL: "u", Title: "t", PublishedAt: now.Add(-time.Hour)}}},
		details: map[string]models.RentPost{
			"a": {Source: "weibo", ExternalID: "a", Title: "t", CollectedAt: now, Status: models.PostStatusCollected},
		},
	}}
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 3600, MaxAgeDays: 7, Sources: []string{"weibo"},
			Weibo: config.WeiboConfig{RangeFrom: "-10", Users: []string{"1234567890"}},
		},
	}, nil)
	r := NewRunner(rt, st, nil, nil)
	if err := st.SetProgress("weibo", store.SourceProgress{
		Page: "", SeenNewest: now.Add(-24 * time.Hour).Format(time.RFC3339Nano),
		Fingerprint: "weibo|-10",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runSourceOnce(context.Background(), src, make(chan struct{}, 2)); err != nil {
		t.Fatal(err)
	}
	wantStart := now.Add(-10 * 24 * time.Hour)
	if src.winStart.IsZero() || src.winStart.After(wantStart.Add(2*time.Minute)) || src.winStart.Before(wantStart.Add(-2*time.Minute)) {
		t.Errorf("列表窗起点 = %v, want 约 %v（-10 天），不应是水位昨天", src.winStart, wantStart)
	}
}

func TestFormatPageCursor(t *testing.T) {
	if got := formatPageCursor(""); got != "组1第1页" {
		t.Errorf("空游标 = %s", got)
	}
	if got := formatPageCursor("1:50"); got != "组2第3页" {
		t.Errorf("1:50 = %s", got)
	}
	if got := formatStartPos(true, ""); got != "追新·组1第1页" {
		t.Errorf("追新起点 = %s", got)
	}
	if got := formatNextPos(store.SourceProgress{SeenNewest: "x"}); got != "追新·组1第1页" {
		t.Errorf("追新下次 = %s", got)
	}
	if got := formatSkipSummary(8, 2, true, false); got != "已存在8 超窗新2 到水位线" {
		t.Errorf("跳过摘要 = %s", got)
	}
	if got := nextGroupMsg("douban", 3, "0:0", "1:0", nil); !strings.Contains(got, "执行完成") || !strings.Contains(got, "组2") {
		t.Errorf("换组日志 = %s", got)
	}
	if got := nextGroupMsg("douban", 3, "0:0", "0:25", nil); got != "" {
		t.Errorf("同组翻页不应打串行日志, got %s", got)
	}
}

func TestFormatRoundScope(t *testing.T) {
	src := &fakeSource{name: "fake"}
	got := formatRoundScope(src)
	if !strings.Contains(got, "组1") || !strings.Contains(got, "范围共") {
		t.Errorf("范围 = %s", got)
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
