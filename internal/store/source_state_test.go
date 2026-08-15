package store

import "testing"

func TestParseSourceProgress(t *testing.T) {
	if p := ParseSourceProgress(""); p.Page != "" || p.SeenNewest != "" {
		t.Errorf("空串 = %+v", p)
	}
	legacy := ParseSourceProgress("1:0")
	if legacy.Page != "1:0" || legacy.CatchingUp() {
		t.Errorf("旧游标 = %+v, want page=1:0 未追新", legacy)
	}
	raw := SourceProgress{Phase: ProgressIncremental, Page: "", Watermark: "2026-08-13T00:00:00Z", RangeKey: "k"}.Encode()
	got := ParseSourceProgress(raw)
	if !got.CatchingUp() || got.SeenNewest != "2026-08-13T00:00:00Z" || got.Fingerprint != "k" {
		t.Errorf("旧 JSON 兼容 = %+v", got)
	}
}

func TestProgressRoundTripAndClear(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	want := SourceProgress{Page: "0:25", Fingerprint: "r"}
	if err := s.SetProgress("douban", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetProgress("douban")
	if err != nil || !ok {
		t.Fatalf("读进度: ok=%v err=%v", ok, err)
	}
	if got.Page != "0:25" || got.CatchingUp() {
		t.Errorf("进度 = %+v", got)
	}
	if err := s.ClearProgress("douban"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetProgress("douban"); err != nil || ok {
		t.Errorf("清除后 ok=%v err=%v, want 无记录", ok, err)
	}
}
