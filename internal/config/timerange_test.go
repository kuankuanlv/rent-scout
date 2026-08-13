package config

import (
	"testing"
	"time"
)

func TestResolveTimeRange(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.Local)

	t.Run("now 与 -10d", func(t *testing.T) {
		start, end, err := ResolveTimeRange("-10d", "now", now)
		if err != nil {
			t.Fatal(err)
		}
		wantStart := now.Add(-10 * 24 * time.Hour)
		if !start.Equal(wantStart) {
			t.Errorf("start = %v, want %v", start, wantStart)
		}
		if !end.Equal(now) {
			t.Errorf("end = %v, want now %v", end, now)
		}
	})

	t.Run("空 to 视为 now；空 from 默认 -10d", func(t *testing.T) {
		start, end, err := ResolveTimeRange("", "", now)
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(now.Add(-10 * 24 * time.Hour)) || !end.Equal(now) {
			t.Errorf("got [%v, %v]", start, end)
		}
	})

	t.Run("纯数字天数相对 now", func(t *testing.T) {
		start, end, err := ResolveTimeRange("7", "now", now)
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(now.Add(-7 * 24 * time.Hour)) || !end.Equal(now) {
			t.Errorf("got [%v, %v]", start, end)
		}
	})

	t.Run("绝对日期", func(t *testing.T) {
		start, end, err := ResolveTimeRange("2026-08-01", "2026-08-10 12:00", now)
		if err != nil {
			t.Fatal(err)
		}
		wantStart := time.Date(2026, 8, 1, 0, 0, 0, 0, now.Location())
		wantEnd := time.Date(2026, 8, 10, 12, 0, 0, 0, now.Location())
		if !start.Equal(wantStart) || !end.Equal(wantEnd) {
			t.Errorf("got [%v, %v], want [%v, %v]", start, end, wantStart, wantEnd)
		}
	})

	t.Run("RFC3339", func(t *testing.T) {
		from := "2026-08-01T00:00:00Z"
		to := "2026-08-05T12:00:00Z"
		start, end, err := ResolveTimeRange(from, to, now)
		if err != nil {
			t.Fatal(err)
		}
		wantStart, _ := time.Parse(time.RFC3339, from)
		wantEnd, _ := time.Parse(time.RFC3339, to)
		if !start.Equal(wantStart) || !end.Equal(wantEnd) {
			t.Errorf("got [%v, %v]", start, end)
		}
	})

	t.Run("from 晚于 to 报错", func(t *testing.T) {
		if _, _, err := ResolveTimeRange("now", "-1d", now); err == nil {
			t.Fatal("期望报错")
		}
	})
}
