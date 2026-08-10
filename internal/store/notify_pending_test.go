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
