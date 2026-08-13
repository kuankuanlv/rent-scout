package store

import "testing"

func TestParseSourceProgress(t *testing.T) {
	if p := ParseSourceProgress(""); p.Phase != ProgressBackfill || p.Page != "" {
		t.Errorf("空串 = %+v, want backfill 空 page", p)
	}
	legacy := ParseSourceProgress("1:0")
	if legacy.Phase != ProgressBackfill || legacy.Page != "1:0" {
		t.Errorf("旧游标 = %+v, want backfill page=1:0", legacy)
	}
	raw := SourceProgress{Phase: ProgressIncremental, Page: "", Watermark: "2026-08-13T00:00:00Z", RangeKey: "k"}.Encode()
	got := ParseSourceProgress(raw)
	if got.Phase != ProgressIncremental || got.Watermark != "2026-08-13T00:00:00Z" || got.RangeKey != "k" {
		t.Errorf("JSON 往返 = %+v", got)
	}
}

func TestProgressRoundTripAndClear(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	want := SourceProgress{Phase: ProgressBackfill, Page: "0:25", RangeKey: "r"}
	if err := s.SetProgress("douban", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetProgress("douban")
	if err != nil || !ok {
		t.Fatalf("读进度: ok=%v err=%v", ok, err)
	}
	if got.Page != "0:25" || got.Phase != ProgressBackfill {
		t.Errorf("进度 = %+v", got)
	}
	if err := s.ClearProgress("douban"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetProgress("douban"); err != nil || ok {
		t.Errorf("清除后 ok=%v err=%v, want 无记录", ok, err)
	}
}
