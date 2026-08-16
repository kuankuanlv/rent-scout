package store

import (
	"strings"
	"testing"
)

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

func TestDecodeWatermarksLegacyAndMap(t *testing.T) {
	legacy := "2026-08-13T00:00:00Z"
	m := DecodeWatermarks(legacy)
	if m["*"] != legacy {
		t.Fatalf("legacy = %v", m)
	}
	if t0 := LookupWatermark(m, "user:1"); t0.IsZero() {
		t.Fatal("旧单值应作为所有目标初值")
	}
	enc := EncodeWatermarks(map[string]string{"user:1": legacy})
	if !strings.Contains(enc, "user:1") {
		t.Fatalf("encode=%s", enc)
	}
	got := DecodeWatermarks(enc)
	if got["user:1"] != legacy {
		t.Fatalf("map roundtrip = %v", got)
	}
	if !LookupWatermark(got, "user:2").IsZero() {
		t.Fatal("已有分键后，未出现的目标不应再吃 * 初值")
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
