package store

import (
	"fmt"
	"testing"
	"time"

	"rent-scout/internal/models"
)

// 拉批：passed 且对任一启用渠道未 sent 的帖子；已全渠道 sent 的不返回
func TestFetchNotifyBatch(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	// 3 帖：p1 passed 无通知记录；p2 passed 已 feishu sent；p3 rejected
	p1 := seedPostWithStatus(t, s, models.PostStatusPassed)
	p2 := seedPostWithStatus(t, s, models.PostStatusPassed)
	p3 := seedPostWithStatus(t, s, models.PostStatusRejected)
	// p2 已发 feishu（sent），但未发 wecom → 仍应被拉出（对 wecom 未 sent）
	if _, err := s.InsertNotification(p2.ID, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(p2.ID, "feishu"); err != nil {
		t.Fatal(err)
	}
	channels := []string{"feishu", "wecom"}
	batch, err := s.FetchNotifyBatch(channels, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, p := range batch {
		ids[p.ID] = true
	}
	if !ids[p1.ID] {
		t.Error("p1（passed 无通知记录）未被拉出")
	}
	if !ids[p2.ID] {
		t.Error("p2（feishu 已发、wecom 未发）应被拉出")
	}
	if ids[p3.ID] {
		t.Error("p3（rejected）不应被拉出")
	}
}

// 终止态排除：全渠道 sent 或 sent+dead 的帖子不再拉出
func TestFetchNotifyBatchTerminalExcluded(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	p1 := seedPostWithStatus(t, s, models.PostStatusPassed) // feishu+wecom 双 sent
	p2 := seedPostWithStatus(t, s, models.PostStatusPassed) // feishu sent + wecom dead
	p3 := seedPostWithStatus(t, s, models.PostStatusPassed) // feishu sent + wecom failed → 应拉出（failed 可重试）
	for _, p := range []models.RentPost{p1, p2, p3} {
		if _, err := s.InsertNotification(p.ID, "feishu"); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkNotificationSent(p.ID, "feishu"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertNotification(p1.ID, "wecom"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationSent(p1.ID, "wecom"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(p2.ID, "wecom"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationDead(p2.ID, "wecom", "webhook 403 超限"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(p3.ID, "wecom"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotificationFailed(p3.ID, "wecom", "网络超时", 1); err != nil {
		t.Fatal(err)
	}
	batch, err := s.FetchNotifyBatch([]string{"feishu", "wecom"}, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, p := range batch {
		ids[p.ID] = true
	}
	if ids[p1.ID] {
		t.Error("p1（全渠道 sent）不应被拉出")
	}
	if ids[p2.ID] {
		t.Error("p2（sent+dead 均为终止态）不应被拉出")
	}
	if !ids[p3.ID] {
		t.Error("p3（feishu sent + wecom failed）应被拉出（failed 可重试）")
	}
}

// requireAI=true：只拉已 AI 审核（ai_result 非空，通过与否都算）的 passed 帖；未审核/无结果的不拉
func TestFetchNotifyBatchRequireAI(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	pPass := seedPostWithStatus(t, s, models.PostStatusPassed) // AI 通过
	pFail := seedPostWithStatus(t, s, models.PostStatusPassed) // AI 未通过
	pNone := seedPostWithStatus(t, s, models.PostStatusPassed) // 有 filter_results 行但无 ai_result（硬筛结果，未走 AI）
	pEmpty := seedPostWithStatus(t, s, models.PostStatusPassed) // 无 filter_results 行
	now := time.Now()
	if err := s.SaveFilterResult(models.FilterResult{PostID: pPass.ID, Status: models.PostStatusPassed, Stage: models.StageAIRule, DecidedAt: now, AI: &models.AIResult{Passed: true, Reason: "靠谱"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: pFail.ID, Status: models.PostStatusPassed, Stage: models.StageAIRule, DecidedAt: now, AI: &models.AIResult{Passed: false, Reason: "像中介"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFilterResult(models.FilterResult{PostID: pNone.ID, Status: models.PostStatusPassed, Stage: models.StageHardRule, DecidedAt: now}); err != nil {
		t.Fatal(err)
	}
	channels := []string{"feishu"}

	idsOf := func(batch []models.RentPost) map[int64]bool {
		ids := map[int64]bool{}
		for _, p := range batch {
			ids[p.ID] = true
		}
		return ids
	}

	withAI, err := s.FetchNotifyBatch(channels, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	ids := idsOf(withAI)
	if !ids[pPass.ID] {
		t.Error("requireAI: AI 通过的帖应被拉出")
	}
	if !ids[pFail.ID] {
		t.Error("requireAI: AI 未通过的帖也应被拉出")
	}
	if ids[pNone.ID] {
		t.Error("requireAI: 有结果行但无 ai_result 的帖不应被拉出")
	}
	if ids[pEmpty.ID] {
		t.Error("requireAI: 无 filter_results 行的帖不应被拉出")
	}

	noAI, err := s.FetchNotifyBatch(channels, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	all := idsOf(noAI)
	for name, p := range map[string]models.RentPost{"AI通过": pPass, "AI未通过": pFail, "未审核": pNone, "无结果行": pEmpty} {
		if !all[p.ID] {
			t.Errorf("不要求 AI 时 %s 帖应被拉出", name)
		}
	}
}

// 批内渠道状态：返回每帖每渠道当前通知状态
func TestNotificationStatuses(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	p := seedPostWithStatus(t, s, models.PostStatusPassed)
	if _, err := s.InsertNotification(p.ID, "feishu"); err != nil {
		t.Fatal(err)
	}
	st, err := s.NotificationStatuses([]int64{p.ID}, []string{"feishu", "wecom"})
	if err != nil {
		t.Fatal(err)
	}
	if st[p.ID]["feishu"] != "pending" {
		t.Errorf("feishu 状态: %q, want pending", st[p.ID]["feishu"])
	}
	if _, ok := st[p.ID]["wecom"]; ok {
		t.Error("wecom 无记录不应出现在状态中")
	}
}

// 辅助：插入指定主状态的帖子并返回完整对象（含自增 ID）
func seedPostWithStatus(t *testing.T, s *Store, status string) models.RentPost {
	t.Helper()
	p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("seed-%d", time.Now().UnixNano()), Title: "t", CollectedAt: time.Now(), Status: status}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM posts WHERE source='douban' ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p.ID = id
	return p
}
