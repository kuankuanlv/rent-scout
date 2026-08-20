package ports

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"rent-scout/internal/config"
)

// ProjectRepoURL 控制台首页/页脚展示的仓库地址
const ProjectRepoURL = "https://github.com/kuankuanlv/rent-scout"

// PageCtx 页面公共数据：Token 透传 + Active 导航高亮 + 登出选项
func PageCtx(rt *config.HotConfig, r *http.Request, active string) map[string]any {
	return map[string]any{
		"Token":      r.URL.Query().Get("token"),
		"Active":     active,
		"RepoURL":    ProjectRepoURL,
		"ShowLogout": rt.Get().Admin.AuthRequired,
	}
}

// MergePageCtx 合并页面数据（base 优先保留 Token/Active）
func MergePageCtx(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// WriteJSON 写 JSON 响应
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSONStatus 写 JSON 响应并指定状态码
func WriteJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WantsJSON 请求是否期望 JSON 响应
func WantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

// FirstNonEmpty 取第一个非空白字符串
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// SourceController 采集源控制接口（admin 不依赖 collector 包）
type SourceController interface {
	SetEnabled(name string, on bool) error
	Sources() []string
	SourceEnabled(name string) bool
}

// CookieCloudInspect CookieCloud 探测结果（handler 不引用 cookie 包类型）
type CookieCloudInspect struct {
	Cookie      string
	Names       []string
	Previews    []string
	Algo        string
	CipherField string
	HTTPStatus  int
	Domains     []string
}

// DoubanPageResult 豆瓣页探测结果
type DoubanPageResult struct {
	OK      bool
	HTTP    int
	Snippet string
}

// CookieProbe 管理台 CookieCloud / 豆瓣探测
type CookieProbe interface {
	InspectCookieCloud(ctx context.Context, draft config.DoubanCookieConfig, source string) (CookieCloudInspect, error)
	ProbePage(ctx context.Context, probeURL, rawCookie string) DoubanPageResult
}

// LLMProbe 管理台 LLM 连通与模型列表
type LLMProbe interface {
	ListModels(ctx context.Context, baseURL, apiKey, model string) ([]string, error)
	Chat(ctx context.Context, baseURL, apiKey, model, system, user string) (string, error)
}

// NotifyProbeItem 试发用的通知条目（不引用 notifier 包）
type NotifyProbeItem struct {
	PostID             int64
	Title              string
	URL                string
	Price              int
	Contact            string
	Commuting          string
	Reason             string
	AddressTag         string
	FeedbackURL        string
	FeedbackUselessURL string
	HandledURL         string
}

// NotifyProbe 管理台飞书/PushPlus 试发
type NotifyProbe interface {
	Send(ctx context.Context, channel, webhook, token, topic string, items []NotifyProbeItem) error
}

// NotifyManual 控制台勾选帖子后直发
type NotifyManual interface {
	SendSelected(ctx context.Context, ids []int64, group string) error
}

// CSVHas 逗号列表是否含某项（模板多选勾选态）
func CSVHas(csv, item string) bool {
	for _, p := range strings.Split(csv, ",") {
		if strings.TrimSpace(p) == item {
			return true
		}
	}
	return false
}
