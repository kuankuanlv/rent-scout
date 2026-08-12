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

// 双链接协议：ProcessBatch 生成的消息同时包含有用和无用反馈链接
func TestDualFeedbackLinksPresent(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var capturedText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if content, ok := body["content"].(map[string]interface{}); ok {
			if text, ok := content["text"].(string); ok {
				capturedText = text
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := NewNotifier(s, NotifierOptions{MaxAttempts: 3}, NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	if capturedText == "" {
		t.Fatal("未捕获到消息文本")
	}

	// 验证双链接都存在于消息文本中（textPayload 格式）
	if !strings.Contains(capturedText, "反馈: 有用") {
		t.Error("消息应包含「有用」反馈链接")
	}
	if !strings.Contains(capturedText, "反馈: 无用") {
		t.Error("消息应包含「无用」反馈链接")
	}
}

// 双链接协议：feedbackSecret 非空时，两个 URL 都包含 exp 和 sig 参数
func TestDualFeedbackLinksWithSignature(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var capturedText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if content, ok := body["content"].(map[string]interface{}); ok {
			if text, ok := content["text"].(string); ok {
				capturedText = text
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 使用非空的 feedbackSecret
	n := NewNotifier(s, NotifierOptions{MaxAttempts: 3, FeedbackSecret: "test-secret-123"}, NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	if capturedText == "" {
		t.Fatal("未捕获到消息文本")
	}

	// 验证两个 URL 都包含签名参数（exp 和 sig）
	lines := strings.Split(capturedText, "\n")
	var usefulURL, uselessURL string
	for _, line := range lines {
		if strings.Contains(line, "反馈: 有用") {
			usefulURL = strings.TrimSpace(strings.TrimPrefix(line, "反馈: 有用 "))
		}
		if strings.Contains(line, "反馈: 无用") {
			uselessURL = strings.TrimSpace(strings.TrimPrefix(line, "反馈: 无用 "))
		}
	}

	if usefulURL == "" {
		t.Fatal("未找到有用反馈 URL")
	}
	if uselessURL == "" {
		t.Fatal("未找到无用反馈 URL")
	}

	if !strings.Contains(usefulURL, "exp=") || !strings.Contains(usefulURL, "sig=") {
		t.Errorf("有用 URL 应包含 exp 和 sig 参数: %s", usefulURL)
	}
	if !strings.Contains(uselessURL, "exp=") || !strings.Contains(uselessURL, "sig=") {
		t.Errorf("无用 URL 应包含 exp 和 sig 参数: %s", uselessURL)
	}
}

// 双链接协议：feedbackSecret 为空时，两个 URL 不包含签名参数（降级模式）
func TestDualFeedbackLinksNoSignature(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var capturedText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if content, ok := body["content"].(map[string]interface{}); ok {
			if text, ok := content["text"].(string); ok {
				capturedText = text
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 使用空的 feedbackSecret（降级模式）
	n := NewNotifier(s, NotifierOptions{MaxAttempts: 3, FeedbackSecret: ""}, NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	if capturedText == "" {
		t.Fatal("未捕获到消息文本")
	}

	// 验证两个 URL 都不包含签名参数
	lines := strings.Split(capturedText, "\n")
	var usefulURL, uselessURL string
	for _, line := range lines {
		if strings.Contains(line, "反馈: 有用") {
			usefulURL = strings.TrimSpace(strings.TrimPrefix(line, "反馈: 有用 "))
		}
		if strings.Contains(line, "反馈: 无用") {
			uselessURL = strings.TrimSpace(strings.TrimPrefix(line, "反馈: 无用 "))
		}
	}

	if usefulURL == "" {
		t.Fatal("未找到有用反馈 URL")
	}
	if uselessURL == "" {
		t.Fatal("未找到无用反馈 URL")
	}

	if strings.Contains(usefulURL, "sig=") {
		t.Errorf("降级模式下有用 URL 不应包含 sig 参数: %s", usefulURL)
	}
	if strings.Contains(uselessURL, "sig=") {
		t.Errorf("降级模式下无用 URL 不应包含 sig 参数: %s", uselessURL)
	}
}
