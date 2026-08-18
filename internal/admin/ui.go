package admin

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"rent-scout/internal/models"
)

// ProjectRepoURL 控制台首页/页脚展示的仓库地址
const ProjectRepoURL = "https://github.com/kuankuanlv/rent-scout"

// pageCtx 页面公共数据：Token 透传 + Active 导航高亮 + 登出选项
func (s *Server) pageCtx(r *http.Request, active string) map[string]any {
	return map[string]any{
		"Token":      r.URL.Query().Get("token"),
		"Active":     active,
		"RepoURL":    ProjectRepoURL,
		"ShowLogout": s.rt.Get().Admin.AuthRequired,
	}
}

// mergePageCtx 合并页面数据（base 优先保留 Token/Active）
func mergePageCtx(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// writeJSON 写 JSON 响应
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

// firstNonEmpty 取第一个非空白字符串
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// statusLabel 帖子主状态码 → 全览页中文（与 collected|pending|passed|rejected 一一对应）
func statusLabel(s string) string {
	switch s {
	case "collected":
		return "已采集"
	case "passed":
		return "通过"
	case "rejected":
		return "拒绝"
	default:
		return s
	}
}

func sourceLabel(s string) string {
	switch s {
	case models.SourceDouban.String():
		return "豆瓣"
	case models.SourceWeibo.String():
		return "微博"
	default:
		return s
	}
}

// csvHas 逗号列表是否含某项（模板多选勾选态）
func csvHas(csv, item string) bool {
	for _, p := range strings.Split(csv, ",") {
		if strings.TrimSpace(p) == item {
			return true
		}
	}
	return false
}

// percent 百分比（保留 1 位小数）；b=0 返回 0
func percent(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return math.Round(float64(a)/float64(b)*1000) / 10
}
