package window

import (
	"fmt"

	"math"
	"strconv"
	"strings"
	"time"
)

// ResolveTimeRange 解析拉取时间窗。
// from/to 按「天」相对 now：负数=过去，正数=未来；now / 空 to = 0。
// 「从」只能为负且必须小于「至」。
func ResolveTimeRange(from, to string, now time.Time) (start, end time.Time, err error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		from = "-10"
	}
	if to == "" {
		to = "now"
	}
	start, fromDays, fromKind, err := parseTimeBound(from, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from %q: %w", from, err)
	}
	end, toDays, toKind, err := parseTimeBound(to, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to %q: %w", to, err)
	}
	if fromKind == boundOffset && fromDays >= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("「从」只能为负数")
	}
	if fromKind == boundOffset && toKind == boundOffset && fromDays >= toDays {
		return time.Time{}, time.Time{}, fmt.Errorf("「从」必须小于「至」")
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("from 不能晚于或等于 to")
	}
	return start, end, nil
}

const (
	boundNow      = "now"
	boundOffset   = "offset"
	boundAbsolute = "absolute"
)

func parseTimeBound(s string, now time.Time) (time.Time, float64, string, error) {
	if strings.EqualFold(s, "now") {
		return now, 0, boundNow, nil
	}
	if days, ok := parseDayOffset(s); ok {
		d := time.Duration(days * float64(24*time.Hour))
		return now.Add(d), days, boundOffset, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, 0, boundAbsolute, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, 0, boundAbsolute, nil
		}
	}
	return time.Time{}, 0, "", fmt.Errorf("无法解析（支持 now/-10/小数天）")
}

// parseDayOffset 认 -10、-10.5、0.5；保留正负号
func parseDayOffset(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || strings.EqualFold(s, "now") {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

// CollectorReplayWindow 规则重放用采集时间窗：豆瓣、微博 RangeFrom 取更早的起点，终点都是 now
func CollectorReplayWindow(doubanFrom, weiboFrom string, now time.Time) (start, end time.Time, err error) {
	froms := []string{doubanFrom, weiboFrom}
	if strings.TrimSpace(doubanFrom) == "" && strings.TrimSpace(weiboFrom) == "" {
		froms = []string{"-10"}
	}
	var lastErr error
	for _, from := range froms {
		s, e, eerr := ResolveTimeRange(from, "now", now)
		if eerr != nil {
			lastErr = eerr
			continue
		}
		if start.IsZero() || s.Before(start) {
			start = s
		}
		end = e
	}
	if start.IsZero() {
		if lastErr != nil {
			return time.Time{}, time.Time{}, lastErr
		}
		return ResolveTimeRange("-10", "now", now)
	}
	return start, end, nil
}

// CanonicalDayOffset 存库/展示：now 保持；绝对时间原样；相对天归一化小数
func CanonicalDayOffset(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "now") {
		return "now"
	}
	if days, ok := parseDayOffset(s); ok {
		return strconv.FormatFloat(days, 'f', -1, 64)
	}
	return s
}
