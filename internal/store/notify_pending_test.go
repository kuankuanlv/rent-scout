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
	batch, err := s.FetchNotifyBatch(channels, 10)
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
	batch, err := s.FetchNotifyBatch([]string{"feishu", "wecom"}, 10)
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
