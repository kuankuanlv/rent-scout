package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// 辅助：notifier 测试自建 store（公开 API）
func newNotifierTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 辅助：插入指定主状态的帖子并查回完整对象（含自增 ID）
func seedNotifierPost(t *testing.T, s *store.Store, status string) models.RentPost {
	t.Helper()
	p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("seed-%d", time.Now().UnixNano()), Title: "t", CollectedAt: time.Now(), Status: status}
	if _, err := s.InsertPost(p); err != nil {
		t.Fatal(err)
	}
	batch, err := s.FetchPendingByStatus(status, 1000)
	if err != nil || len(batch) == 0 {
		t.Fatalf("播种失败: err=%v", err)
	}
	return batch[len(batch)-1]
}

// 全流程：2 帖 passed（望京/未分组）→ feishu 成功 → 状态 sent，分两组各发 1 条
func TestNotifierProcessBatch(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p1 := seedNotifierPost(t, s, models.PostStatusPassed)
	p1.AddressTags = []string{"望京"}
	if err := s.UpdatePostAddressTags(p1.ID, p1.AddressTags); err != nil {
		t.Fatal(err)
	}
	p2 := seedNotifierPost(t, s, models.PostStatusPassed)

	feishuHits := int64(0)
	fsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&feishuHits, 1)
		w.WriteHeader(200)
	}))
	defer fsrv.Close()

	n := NewNotifier(s, NotifierOptions{MaxAttempts: 3}, NewFeishuChannel(fsrv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p1, p2})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&feishuHits) != 2 {
		t.Errorf("飞书应发 2 条（望京组+未分组组）: %d", feishuHits)
	}
	// 状态检查：两帖 feishu 均 sent
	for _, p := range []models.RentPost{p1, p2} {
		st, err := s.NotificationStatuses([]int64{p.ID}, []string{ChannelFeishu})
		if err != nil {
			t.Fatal(err)
		}
		if st[p.ID][ChannelFeishu] != "sent" {
			t.Errorf("post %d feishu 状态: %q, want sent", p.ID, st[p.ID][ChannelFeishu])
		}
	}
}

// 失败重试：渠道失败 → failed + attempts 递增；attempts 达 MaxAttempts → dead 且不再发送
func TestNotifierRetryThenDead(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	n := NewNotifier(s, NotifierOptions{MaxAttempts: 2}, NewFeishuChannel(srv.URL))
	// 第一轮：发送失败 → failed attempt=1
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p}); err == nil {
		t.Error("发送失败应返回 err")
	}
	// 第二轮：attempt=2 达阈值 → dead
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p}); err == nil {
		t.Error("第二轮失败应返回 err")
	}
	// 第三轮：dead 不再发送 → 无 err
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p}); err != nil {
		t.Fatal(err)
	}
	st, err := s.NotificationStatuses([]int64{p.ID}, []string{ChannelFeishu})
	if err != nil {
		t.Fatal(err)
	}
	if st[p.ID][ChannelFeishu] != "dead" {
		t.Errorf("feishu 状态: %q, want dead", st[p.ID][ChannelFeishu])
	}
}

// 部分成功：望京组成功、回龙观组失败 → 组隔离（调整 B 3.1），失败组达阈值 dead
func TestNotifierGroupIsolation(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	pA := seedNotifierPost(t, s, models.PostStatusPassed)
	pA.AddressTags = []string{"望京"}
	_ = s.UpdatePostAddressTags(pA.ID, pA.AddressTags)
	pB := seedNotifierPost(t, s, models.PostStatusPassed)
	pB.AddressTags = []string{"回龙观"}
	_ = s.UpdatePostAddressTags(pB.ID, pB.AddressTags)

	// 按 body 内容区分组（不依赖发送顺序）：望京 200、其余 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := ""
		if c, ok := body["content"].(map[string]interface{}); ok {
			if txt, ok := c["text"].(string); ok {
				content = txt
			}
		}
		if strings.Contains(content, "望京") {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()

	n := NewNotifier(s, NotifierOptions{MaxAttempts: 1}, NewFeishuChannel(srv.URL))
	_ = n.ProcessBatch(context.Background(), []models.RentPost{pA, pB})
	st, err := s.NotificationStatuses([]int64{pA.ID, pB.ID}, []string{ChannelFeishu})
	if err != nil {
		t.Fatal(err)
	}
	if st[pA.ID][ChannelFeishu] != "sent" {
		t.Errorf("望京组应 sent: %q", st[pA.ID][ChannelFeishu])
	}
	if st[pB.ID][ChannelFeishu] != "dead" {
		t.Errorf("回龙观组失败应 dead（MaxAttempts=1）: %q", st[pB.ID][ChannelFeishu])
	}
}
