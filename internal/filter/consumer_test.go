package filter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// 消费批：硬编码通过 → 状态 passed + AddressTags 写回 + notify 信号
func TestConsumerProcessBatch(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 白名单规则（地点）：Consumer 从 rules 表热拉取（c.rules → ListRules(true)），须先入库
	chain := NewRuleChain(nil)
	if _, err := st.CreateRule(models.Rule{Name: "望京", Type: models.RuleTypeHardWhitelist,
		Mode: models.RuleModeInclude, Value: "望京", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}

	// 入库两条：一白名单命中，一未命中（无 AI → 默认放行）
	p1 := models.RentPost{Source: "douban", ExternalID: "a", Title: "望京整租", Content: "近望京地铁",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	p2 := models.RentPost{Source: "douban", ExternalID: "b", Title: "回龙观", Content: "两居",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	st.InsertPost(p1)
	st.InsertPost(p2)

	notify := make(chan struct{}, 10)
	c := NewConsumer(chain, st, notify, 500)
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.processBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}

	// 状态断言：两条都 passed（无 AI 默认放行）
	rows, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
	if len(rows) != 2 {
		t.Fatalf("passed 数 = %d, want 2", len(rows))
	}
	// AddressTags：p1 命中望京（Consumer 已写库 posts.address_tags）
	var tags []string
	for _, p := range rows {
		if p.ExternalID == "a" {
			tags = p.AddressTags
		}
	}
	if len(tags) != 1 || tags[0] != "望京" {
		t.Errorf("p1 标签 = %v, want [望京]", tags)
	}
	// notify 信号：passed 帖子触发（≥1）
	select {
	case <-notify:
	default:
		t.Error("passed 应触发 notify 信号")
	}
	// 筛选结果已写
	if _, ok, _ := st.FilterResultByPostID(rows[0].ID); !ok {
		t.Error("filter_results 未写")
	}
}

// 黑名单拒绝：状态 rejected + 拒绝原因记录
func TestConsumerRejects(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	defer st.Close()
	// 黑名单规则：Consumer 从 rules 表热拉取，须先入库
	chain := NewRuleChain(nil)
	if _, err := st.CreateRule(models.Rule{Name: "中介", Type: models.RuleTypeHardBlacklist,
		Mode: models.RuleModeExclude, Value: "中介", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	p := models.RentPost{Source: "douban", ExternalID: "c", Title: "中介勿扰", Content: "中介勿扰，代理绕行",
		CollectedAt: time.Now(), Status: models.PostStatusCollected}
	st.InsertPost(p)

	c := NewConsumer(chain, st, nil, 500)
	batch, _ := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err := c.processBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	// collected 清空
	batch2, _ := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if len(batch2) != 0 {
		t.Errorf("collected 应清空, got %d", len(batch2))
	}
	// filter_results 记录拒绝
	res, ok, err := st.FilterResultByPostID(1)
	if err != nil || !ok {
		t.Fatalf("结果缺失: ok=%v err=%v", ok, err)
	}
	if res.Status != models.PostStatusRejected || !contains(res.RejectedBy, "中介") {
		t.Errorf("拒绝记录错误: %+v", res)
	}
}

// fakeAIEvaluator 测试用：实现 AIEvaluator 接口，计数 EvaluateBatch 调用、记录入参，可注入整批失败。
// results 按 PostID 预置判定（EvaluateAIBatch 要求每帖都有结果）；err 非 nil 时整批返回错误；
// failOnce = 仅第一次调用失败（子批拆分容错断言用）
type fakeAIEvaluator struct {
	calls    int
	batches  [][]models.RentPost // 每次调用的子批（子批拆分断言用）
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

// setupAIConsumer 构造走 AI 批的消费器：1 条 AI 自然语言规则（启用 AI 批路径）+ 3 条未定案帖
// （不命中任何硬编码规则 → 全部进 AI 批）。返回 consumer、store、批量帖与 notify 通道
func setupAIConsumer(t *testing.T, ai AIEvaluator, opts ConsumerOptions) (*Consumer, *store.Store, []models.RentPost, chan struct{}) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// AI 自然语言规则：Consumer 从 rules 表热拉取（c.rules → ListRules(true)），须先入库
	if _, err := st.CreateRule(models.Rule{Name: "AI筛选", Type: models.RuleTypeAINatural,
		Mode: models.RuleModeExclude, Value: "只要地铁 1 公里内的整租", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	for _, eid := range []string{"a1", "a2", "a3"} {
		if _, err := st.InsertPost(models.RentPost{Source: "douban", ExternalID: eid,
			Title: "回龙观两居", Content: "五环外普通两居", CollectedAt: time.Now(), Status: models.PostStatusCollected}); err != nil {
			t.Fatal(err)
		}
	}
	notify := make(chan struct{}, 10)
	c := NewConsumerWithOptions(NewRuleChain(ai), st, notify, 500, opts)
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	return c, st, batch, notify
}

// AI 聚合路径（规格 5.4/5.6 全局约束）：未定案帖子聚合为一次 EvaluateAIBatch 调用（杜绝每帖一次 LLM），
// 结果正确流转到 passed/rejected + filter_results + notify；AI 失败则整批保持 pending 不误标记
func TestConsumerAIBatch(t *testing.T) {
	t.Run("单次调用且结果正确流转", func(t *testing.T) {
		fake := &fakeAIEvaluator{}
		c, st, batch, notify := setupAIConsumer(t, fake, ConsumerOptions{})
		// AI 判定：a1/a3 通过，a2 拒绝（超预算）
		fake.results = map[int64]*models.AIResult{
			batch[0].ID: {Passed: true, Reason: "位置好", Price: 4500},
			batch[1].ID: {Passed: false, Reason: "超出预算", Price: 9000},
			batch[2].ID: {Passed: true, Reason: "通勤方便", Price: 4200},
		}
		if err := c.processBatch(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		// 核心：多帖未定案只触发 1 次 EvaluateBatch（非每帖一次 LLM 调用）
		if fake.calls != 1 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 1（整批一次）", fake.calls)
		}
		if len(fake.gotPosts) != 3 {
			t.Errorf("AI 批帖子数 = %d, want 3（整批聚合）", len(fake.gotPosts))
		}
		// 状态与结果：passed 帖 → passed + filter_results + notify；rejected 帖 → rejected + 原因
		want := map[string]string{
			batch[0].ExternalID: models.PostStatusPassed,
			batch[1].ExternalID: models.PostStatusRejected,
			batch[2].ExternalID: models.PostStatusPassed,
		}
		for _, p := range batch {
			res, ok, err := st.FilterResultByPostID(p.ID)
			if err != nil || !ok {
				t.Fatalf("post %d filter_results 缺失: ok=%v err=%v", p.ID, ok, err)
			}
			if res.Status != want[p.ExternalID] {
				t.Errorf("post %d 状态 = %s, want %s", p.ID, res.Status, want[p.ExternalID])
			}
			if res.Stage != models.StageAIRule || res.AI == nil {
				t.Errorf("post %d 应记录 AI 阶段结果: %+v", p.ID, res)
			}
			if p.ExternalID == "a2" && !contains(res.RejectedBy, "AI拒绝") {
				t.Errorf("a2 拒绝原因 = %q, want 含 AI拒绝", res.RejectedBy)
			}
		}
		// posts 主状态一致（2 passed）+ notify 信号
		passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
		if len(passed) != 2 {
			t.Errorf("passed 数 = %d, want 2", len(passed))
		}
		select {
		case <-notify:
		default:
			t.Error("passed 帖应触发 notify 信号")
		}
	})

	t.Run("失败整批保持 pending", func(t *testing.T) {
		fake := &fakeAIEvaluator{err: context.DeadlineExceeded}
		c, st, batch, notify := setupAIConsumer(t, fake, ConsumerOptions{})
		if err := c.processBatch(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 1 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 1", fake.calls)
		}
		// 整批保持 pending 下轮重试，不误标 passed/rejected（规格 5.6）
		pending, _ := st.FetchPendingByStatus(models.PostStatusPending, 10)
		if len(pending) != 3 {
			t.Errorf("pending 数 = %d, want 3（整批保持待判定）", len(pending))
		}
		// 不写 filter_results、不触发 notify
		for _, p := range batch {
			if _, ok, err := st.FilterResultByPostID(p.ID); err != nil || ok {
				t.Errorf("post %d 失败批不应写 filter_results: ok=%v err=%v", p.ID, ok, err)
			}
		}
		select {
		case <-notify:
			t.Error("失败批不应触发 notify")
		default:
		}
	})
}

// I1（最终审查）：ai_batch_size 接线为 AI 子批上限——未定案帖数 > ai_batch_size 时
// 拆分为多次 EvaluateAIBatch 调用（每次 ≤ ai_batch_size），不再整批一次
func TestConsumerAISubBatchSplit(t *testing.T) {
	t.Run("超过上限拆分为多次调用", func(t *testing.T) {
		fake := &fakeAIEvaluator{}
		c, st, batch, _ := setupAIConsumer(t, fake, ConsumerOptions{AIBatchSize: 2})
		fake.results = map[int64]*models.AIResult{}
		for _, p := range batch {
			fake.results[p.ID] = &models.AIResult{Passed: true, Reason: "ok", Price: 4000}
		}
		if err := c.processBatch(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		// 3 帖 / 上限 2 → 2 次调用（2+1），每次 ≤ 2
		if fake.calls != 2 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 2（3 帖按上限 2 拆分）", fake.calls)
		}
		for i, sub := range fake.batches {
			if len(sub) > 2 {
				t.Errorf("子批 %d 帖数 = %d, want ≤ 2", i, len(sub))
			}
		}
		if len(fake.batches) != 2 || len(fake.batches[0]) != 2 || len(fake.batches[1]) != 1 {
			t.Errorf("子批划分 = %v, want [2 1]", lens(fake.batches))
		}
		// 全部判定正常流转 passed
		passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
		if len(passed) != 3 {
			t.Errorf("passed 数 = %d, want 3", len(passed))
		}
	})
	t.Run("未超上限整批一次调用", func(t *testing.T) {
		fake := &fakeAIEvaluator{}
		c, _, batch, _ := setupAIConsumer(t, fake, ConsumerOptions{AIBatchSize: 10})
		fake.results = map[int64]*models.AIResult{}
		for _, p := range batch {
			fake.results[p.ID] = &models.AIResult{Passed: true, Reason: "ok", Price: 4000}
		}
		if err := c.processBatch(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		if fake.calls != 1 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 1（未超上限不拆分）", fake.calls)
		}
	})
	t.Run("单子批失败仅该子批保持 pending", func(t *testing.T) {
		fake := &fakeAIEvaluator{failOnce: true}
		c, st, batch, _ := setupAIConsumer(t, fake, ConsumerOptions{AIBatchSize: 2})
		fake.results = map[int64]*models.AIResult{}
		for _, p := range batch {
			fake.results[p.ID] = &models.AIResult{Passed: true, Reason: "ok", Price: 4000}
		}
		if err := c.processBatch(context.Background(), batch); err != nil {
			t.Fatal(err)
		}
		// 第一个子批（2 帖）失败 → 该子批 pending；第二个子批（1 帖）继续成功 → passed。
		// 子批相互独立，失败不拖累后续子批（规格 5.6 仅失败部分待重试）
		if fake.calls != 2 {
			t.Errorf("EvaluateBatch 调用次数 = %d, want 2（失败后仍继续下一子批）", fake.calls)
		}
		pending, _ := st.FetchPendingByStatus(models.PostStatusPending, 10)
		passed, _ := st.FetchPendingByStatus(models.PostStatusPassed, 10)
		if len(pending) != 2 || len(passed) != 1 {
			t.Errorf("pending=%d passed=%d, want pending=2 passed=1（仅失败子批待重试）", len(pending), len(passed))
		}
	})
}

func lens(batches [][]models.RentPost) []int {
	out := make([]int, len(batches))
	for i, b := range batches {
		out[i] = len(b)
	}
	return out
}

// K1（最终审查）：rules 读取失败（DB 层故障）→ 整批保持待判定，不流转、不写 filter_results、不发 notify。
// 规格 5.6 仅授权"AI 链不可用/无启用规则"时默认放行；DB 故障不得静默放行
func TestConsumerRulesReadErrorKeepsBatchPending(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rules-fail.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// AI 已启用 + AI 规则已入库：正常路径应走 AI 批而非宽松放行
	chain := NewRuleChain(&fakeAIEvaluator{})
	if _, err := st.CreateRule(models.Rule{Name: "AI筛选", Type: models.RuleTypeAINatural,
		Mode: models.RuleModeExclude, Value: "只要地铁1公里内", Enabled: true, Priority: 10}); err != nil {
		t.Fatal(err)
	}
	for _, eid := range []string{"r1", "r2"} {
		if _, err := st.InsertPost(models.RentPost{Source: "douban", ExternalID: eid,
			Title: "回龙观两居", Content: "五环外普通两居", CollectedAt: time.Now(), Status: models.PostStatusCollected}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	notify := make(chan struct{}, 10)
	c := NewConsumer(chain, st, notify, 500)

	// 注入规则读取故障：关闭 DB → c.rules()（ListRules）报错
	st.Close()

	if err := c.processBatch(context.Background(), batch); err != nil {
		t.Fatalf("processBatch 应只记录告警不返回 error: %v", err)
	}

	// 用新连接验证（已关闭的 store 不能再查）：状态无变更、无 filter_results、无 notify
	verify, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close()
	// ① 帖子仍为 collected（未流转到 passed/rejected）
	still, err := verify.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(still) != 2 {
		t.Errorf("collected 数 = %d, want 2（整批保持待判定，不流转）", len(still))
	}
	if passed, _ := verify.FetchPendingByStatus(models.PostStatusPassed, 10); len(passed) != 0 {
		t.Errorf("passed 数 = %d, want 0（DB 故障不得默认放行）", len(passed))
	}
	if rejected, _ := verify.FetchPendingByStatus(models.PostStatusRejected, 10); len(rejected) != 0 {
		t.Errorf("rejected 数 = %d, want 0", len(rejected))
	}
	// ② 无 filter_results
	for _, p := range batch {
		if _, ok, err := verify.FilterResultByPostID(p.ID); err != nil || ok {
			t.Errorf("post %d 规则读取失败批不应写 filter_results: ok=%v err=%v", p.ID, ok, err)
		}
	}
	// ③ 无 notify 信号
	select {
	case <-notify:
		t.Error("规则读取失败批不应触发 notify")
	default:
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
