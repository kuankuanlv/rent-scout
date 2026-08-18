package notifier_test

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

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/notifier"
	"rent-scout/internal/notifier/channels"
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

// 从飞书 post 富文本里抠 a 标签：链接文案 → href
func feishuPostLinks(body map[string]interface{}) map[string]string {
	out := map[string]string{}
	content, _ := body["content"].(map[string]interface{})
	post, _ := content["post"].(map[string]interface{})
	zh, _ := post["zh_cn"].(map[string]interface{})
	paras, _ := zh["content"].([]interface{})
	for _, p := range paras {
		nodes, _ := p.([]interface{})
		for _, n := range nodes {
			m, _ := n.(map[string]interface{})
			if m["tag"] != "a" {
				continue
			}
			text, _ := m["text"].(string)
			href, _ := m["href"].(string)
			if text != "" && href != "" {
				out[text] = href
			}
		}
	}
	return out
}

// 全流程：2 帖 passed（望京/未分组）→ feishu 成功 → 状态 sent，分两组各发 1 条
func TestNotifierProcessBatch(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p1 := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := s.ReplaceSystemTags(p1.ID, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	p2 := seedNotifierPost(t, s, models.PostStatusPassed)

	feishuHits := int64(0)
	fsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&feishuHits, 1)
		w.WriteHeader(200)
	}))
	defer fsrv.Close()

	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3}, channels.NewFeishuChannel(fsrv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p1, p2})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&feishuHits) != 2 {
		t.Errorf("飞书应发 2 条（望京组+未分组组）: %d", feishuHits)
	}
	// 状态检查：两帖 feishu 均 sent
	for _, p := range []models.RentPost{p1, p2} {
		st, err := s.NotificationStatuses([]int64{p.ID}, []string{notifier.ChannelFeishu})
		if err != nil {
			t.Fatal(err)
		}
		if st[p.ID][notifier.ChannelFeishu] != "sent" {
			t.Errorf("post %d feishu 状态: %q, want sent", p.ID, st[p.ID][notifier.ChannelFeishu])
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

	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 2}, channels.NewFeishuChannel(srv.URL))
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
	st, err := s.NotificationStatuses([]int64{p.ID}, []string{notifier.ChannelFeishu})
	if err != nil {
		t.Fatal(err)
	}
	if st[p.ID][notifier.ChannelFeishu] != "dead" {
		t.Errorf("feishu 状态: %q, want dead", st[p.ID][notifier.ChannelFeishu])
	}
}

// 部分成功：望京组成功、回龙观组失败 → 组隔离（调整 B 3.1），失败组达阈值 dead
func TestNotifierGroupIsolation(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	pA := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := s.ReplaceSystemTags(pA.ID, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	pB := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := s.ReplaceSystemTags(pB.ID, []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}

	// 按 body 内容区分组（不依赖发送顺序）：望京 200、其余 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "望京") {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()

	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 1}, channels.NewFeishuChannel(srv.URL))
	_ = n.ProcessBatch(context.Background(), []models.RentPost{pA, pB})
	st, err := s.NotificationStatuses([]int64{pA.ID, pB.ID}, []string{notifier.ChannelFeishu})
	if err != nil {
		t.Fatal(err)
	}
	if st[pA.ID][notifier.ChannelFeishu] != "sent" {
		t.Errorf("望京组应 sent: %q", st[pA.ID][notifier.ChannelFeishu])
	}
	if st[pB.ID][notifier.ChannelFeishu] != "dead" {
		t.Errorf("回龙观组失败应 dead（MaxAttempts=1）: %q", st[pB.ID][notifier.ChannelFeishu])
	}
}

// 双链接协议：ProcessBatch 生成的消息同时包含有用和无用反馈链接
func TestDualFeedbackLinksPresent(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var links map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		links = feishuPostLinks(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3}, channels.NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	if links["有用"] == "" {
		t.Error("消息应包含「有用」反馈链接")
	}
	if links["无用"] == "" {
		t.Error("消息应包含「无用」反馈链接")
	}
	if links["标记已读"] == "" {
		t.Error("消息应包含「标记已读」链接")
	}
	for name, href := range links {
		if !strings.Contains(href, "http://") && !strings.Contains(href, "https://") {
			t.Errorf("%s 链接应带可点击的 http(s) 前缀: %s", name, href)
		}
	}
}

// 双链接协议：feedbackSecret 非空时，两个 URL 都包含 exp 和 sig 参数
func TestDualFeedbackLinksWithSignature(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var links map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		links = feishuPostLinks(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 使用非空的 feedbackSecret
	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3, FeedbackSecret: "test-secret-123"}, channels.NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	usefulURL := links["有用"]
	uselessURL := links["无用"]
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

	handledURL := links["标记已读"]
	if handledURL == "" {
		t.Fatal("未找到已处理 URL")
	}
	if !strings.Contains(handledURL, "/h?p=") || strings.Contains(handledURL, "post=") {
		t.Errorf("已处理 URL 应走 /h?p=: %s", handledURL)
	}
	if !strings.Contains(handledURL, "exp=") || !strings.Contains(handledURL, "sig=") {
		t.Errorf("已处理 URL 应包含 exp 和 sig: %s", handledURL)
	}
}

// 双链接协议：feedbackSecret 为空时，两个 URL 不包含签名参数（降级模式）
func TestDualFeedbackLinksNoSignature(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p := seedNotifierPost(t, s, models.PostStatusPassed)

	var links map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		links = feishuPostLinks(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 使用空的 feedbackSecret（降级模式）
	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3, FeedbackSecret: ""}, channels.NewFeishuChannel(srv.URL))
	err := n.ProcessBatch(context.Background(), []models.RentPost{p})
	if err != nil {
		t.Fatal(err)
	}

	usefulURL := links["有用"]
	uselessURL := links["无用"]
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

// 签名密钥跟 HotConfig：改 token 后新通知用新密钥；鉴权关则不签名
func TestFeedbackSecretFollowsHotConfig(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()

	app := &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true, Token: "tok-v1"}}
	rt := config.NewHotConfigWithSnapshot(app, nil)

	var links map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		links = feishuPostLinks(body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3, HotConfig: rt}, channels.NewFeishuChannel(srv.URL))

	p1 := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p1}); err != nil {
		t.Fatal(err)
	}
	u1 := links["有用"]
	if u1 == "" || !strings.Contains(u1, "sig=") {
		t.Fatalf("鉴权开应签名: %q", u1)
	}

	app.Admin.Token = "tok-v2"
	p2 := seedNotifierPost(t, s, models.PostStatusPassed)
	links = nil
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p2}); err != nil {
		t.Fatal(err)
	}
	u2 := links["有用"]
	if u2 == "" || !strings.Contains(u2, "sig=") {
		t.Fatalf("换 token 后仍应签名: %q", u2)
	}
	if u1 == u2 {
		t.Errorf("换 token 后签名 URL 应变化")
	}

	app.Admin.AuthRequired = false
	p3 := seedNotifierPost(t, s, models.PostStatusPassed)
	links = nil
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p3}); err != nil {
		t.Fatal(err)
	}
	u3 := links["有用"]
	if u3 == "" {
		t.Fatal("未找到有用反馈 URL")
	}
	if strings.Contains(u3, "sig=") {
		t.Errorf("鉴权关不应签名: %s", u3)
	}
}

type captureChan struct {
	name  string
	sends [][]notifier.NotifyItem
}

func (c *captureChan) Name() string { return c.name }

func (c *captureChan) Send(ctx context.Context, items []notifier.NotifyItem) ([]int64, []error, error) {
	cp := append([]notifier.NotifyItem(nil), items...)
	c.sends = append(c.sends, cp)
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.PostID
	}
	return ids, nil, nil
}

// 手动直发：不同地点也只发一组；已 sent 的帖也会再发
func TestProcessManualOneGroup(t *testing.T) {
	s := newNotifierTestStore(t)
	defer s.Close()
	p1 := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := s.ReplaceSystemTags(p1.ID, []models.PostTag{{Kind: models.TagKindLocation, Text: "望京", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}
	p2 := seedNotifierPost(t, s, models.PostStatusPassed)
	if err := s.ReplaceSystemTags(p2.ID, []models.PostTag{{Kind: models.TagKindLocation, Text: "回龙观", Source: models.TagSourceSystem}}); err != nil {
		t.Fatal(err)
	}

	ch := &captureChan{name: notifier.ChannelFeishu}
	n := notifier.NewNotifier(s, notifier.NotifierOptions{MaxAttempts: 3}, ch)
	if err := n.ProcessBatch(context.Background(), []models.RentPost{p1, p2}); err != nil {
		t.Fatal(err)
	}
	if len(ch.sends) != 2 {
		t.Fatalf("自动应按地点发 2 组, got %d", len(ch.sends))
	}

	ch.sends = nil
	group := "手动触发-081812:01:30"
	if err := n.ProcessManual(context.Background(), []models.RentPost{p1, p2}, group); err != nil {
		t.Fatal(err)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("手动应只发 1 组, got %d", len(ch.sends))
	}
	items := ch.sends[0]
	if len(items) != 2 {
		t.Fatalf("一组应含 2 帖, got %d", len(items))
	}
	for _, it := range items {
		if it.AddressTag != group {
			t.Errorf("post %d AddressTag=%q, want %q", it.PostID, it.AddressTag, group)
		}
	}
}
