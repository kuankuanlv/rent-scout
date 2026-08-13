package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ResolveTimeRange 解析拉取时间窗。
// from/to：now 或空 to = 动态现在；-Nd / 纯数字天数 = 相对 now 往前；
// 也可填 RFC3339、2006-01-02、2006-01-02 15:04 等绝对时间。
func ResolveTimeRange(from, to string, now time.Time) (start, end time.Time, err error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		from = "-10d"
	}
	if to == "" {
		to = "now"
	}
	start, err = parseTimeBound(from, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from %q: %w", from, err)
	}
	end, err = parseTimeBound(to, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to %q: %w", to, err)
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("from 不能晚于 to")
	}
	return start, end, nil
}

func parseTimeBound(s string, now time.Time) (time.Time, error) {
	if strings.EqualFold(s, "now") {
		return now, nil
	}
	if days, ok := parseRelativeDays(s); ok {
		return now.Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析（支持 now/-10d/天数/日期）")
}

// parseRelativeDays 认 -10d、-10、10；返回正天数（相对 now 往前）
func parseRelativeDays(s string) (int, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = -n
	}
	return n, true
}
