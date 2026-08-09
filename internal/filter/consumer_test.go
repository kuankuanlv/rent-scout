package filter

import (
	"context"
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
