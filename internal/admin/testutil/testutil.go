// Package testutil 提供 admin 模块测试共用的构造辅助（仅测试引用，不进入生产二进制）。
package testutil

import (
	"context"
	"path/filepath"
	"testing"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/filter/ai/llm"
	"rent-scout/internal/store"
)

// NewAdminTestStore 管理面测试用 store 实例（admin 测试需要真实 db 播种数据）
func NewAdminTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// NewTestHotConfig 写入 setup 完成标记并加载 HotConfig
func NewTestHotConfig(t *testing.T, s *store.Store, app *config.AppConfig, token string) *config.HotConfig {
	t.Helper()
	if app == nil {
		app = config.DefaultApp()
	}
	if token != "" {
		app.Admin.Token = token
	}
	kv := config.MergeKV(config.AppToKV(app), config.SecretsToKV(config.DefaultSecrets()))
	kv["setup.completed"] = "true"
	if err := store.SetConfigBatch(s, kv); err != nil {
		t.Fatal(err)
	}
	rt := config.NewHotConfig(s)
	if err := rt.ReloadOnce(); err != nil {
		t.Fatal(err)
	}
	return rt
}

// TestCookieProbe 直连真实 cookie 探测实现
type TestCookieProbe struct{}

func (TestCookieProbe) InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (ports.CookieCloudInspect, error) {
	ins, err := cookie.InspectCookieCloudFor(ctx, draft, source)
	return ports.CookieCloudInspect{
		Cookie: ins.Cookie, Names: ins.Names, Previews: ins.Previews,
		Algo: ins.Algo, CipherField: ins.CipherField, HTTPStatus: ins.HTTPStatus, Domains: ins.Domains,
	}, err
}

func (TestCookieProbe) ProbePage(ctx context.Context, probeURL, rawCookie string) ports.DoubanPageResult {
	page := cookie.ProbePage(ctx, probeURL, rawCookie, nil)
	return ports.DoubanPageResult{OK: page.OK, HTTP: page.HTTP, Snippet: page.Snippet}
}

// TestLLMProbe 直连真实 LLM 客户端
type TestLLMProbe struct{}

func (TestLLMProbe) ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error) {
	return llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true}).ListModels(ctx)
}

func (TestLLMProbe) Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error) {
	return llm.NewClient(llm.ClientOptions{BaseURL: baseURL, APIKey: apiKey, Model: model, DumpHTTP: true}).Chat(ctx, system, user)
}

// StubNotifyProbe 记录最近一次通知探测调用
type StubNotifyProbe struct {
	Channel string
	Items   []ports.NotifyProbeItem
	Err     error
}

func (s *StubNotifyProbe) Send(ctx context.Context, channel, webhook, token, topic string, items []ports.NotifyProbeItem) error {
	s.Channel = channel
	s.Items = items
	return s.Err
}

// PostID 按外部 ID 查找帖子 ID
func PostID(t *testing.T, s *store.Store, externalID string) int64 {
	t.Helper()
	all, err := s.ListPosts(store.PostListFilter{}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if p.ExternalID == externalID {
			return p.ID
		}
	}
	t.Fatalf("帖子 %s 未找到", externalID)
	return 0
}
