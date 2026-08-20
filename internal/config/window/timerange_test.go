package window

import (
	"testing"
	"time"

)

func TestResolveTimeRange(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.Local)

	t.Run("now 与 -10", func(t *testing.T) {
		start, end, err := ResolveTimeRange("-10", "now", now)
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

	t.Run("-10d 不再支持", func(t *testing.T) {
		if _, _, err := ResolveTimeRange("-10d", "now", now); err == nil {
			t.Fatal("应拒绝 -10d")
		}
	})

	t.Run("空 to 视为 now；空 from 默认 -10", func(t *testing.T) {
		start, end, err := ResolveTimeRange("", "", now)
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(now.Add(-10*24*time.Hour)) || !end.Equal(now) {
			t.Errorf("got [%v, %v]", start, end)
		}
	})

	t.Run("小数天", func(t *testing.T) {
		start, end, err := ResolveTimeRange("-1.5", "0.5", now)
		if err != nil {
			t.Fatal(err)
		}
		wantStart := now.Add(-time.Duration(1.5 * float64(24*time.Hour)))
		wantEnd := now.Add(time.Duration(0.5 * float64(24*time.Hour)))
		if !start.Equal(wantStart) || !end.Equal(wantEnd) {
			t.Errorf("got [%v, %v], want [%v, %v]", start, end, wantStart, wantEnd)
		}
	})

	t.Run("至为负", func(t *testing.T) {
		start, end, err := ResolveTimeRange("-10", "-1", now)
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(now.Add(-10*24*time.Hour)) || !end.Equal(now.Add(-24*time.Hour)) {
			t.Errorf("got [%v, %v]", start, end)
		}
	})

	t.Run("从必须为负", func(t *testing.T) {
		if _, _, err := ResolveTimeRange("2", "now", now); err == nil {
			t.Fatal("期望报错")
		}
		if _, _, err := ResolveTimeRange("0", "1", now); err == nil {
			t.Fatal("从=0 应报错")
		}
	})

	t.Run("从必须小于至", func(t *testing.T) {
		if _, _, err := ResolveTimeRange("-1", "-2", now); err == nil {
			t.Fatal("期望报错")
		}
	})

	t.Run("绝对日期仍可用", func(t *testing.T) {
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
}

func TestCanonicalDayOffset(t *testing.T) {
	if got := CanonicalDayOffset("-10d"); got != "-10d" {
		t.Errorf("got %q", got)
	}
	if got := CanonicalDayOffset("now"); got != "now" {
		t.Errorf("got %q", got)
	}
	if got := CanonicalDayOffset("-10.50"); got != "-10.5" {
		t.Errorf("got %q", got)
	}
}

func TestCollectorReplayWindowTakesEarlierFrom(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.Local)
	start, end, err := CollectorReplayWindow("-3", "-10", now)
	if err != nil {
		t.Fatal(err)
	}
	if !start.Equal(now.Add(-10 * 24 * time.Hour)) {
		t.Errorf("start = %v, want 微博更早的 -10 天", start)
	}
	if !end.Equal(now) {
		t.Errorf("end = %v, want now", end)
	}
}
