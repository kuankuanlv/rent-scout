package admin

import (
	"math"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/models"
)

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
	return ports.CSVHas(csv, item)
}

// percent 百分比（保留 1 位小数）；b=0 返回 0
func percent(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return math.Round(float64(a)/float64(b)*1000) / 10
}
