package store

import (
	"strings"
	"testing"
)

func TestParseSourceProgress(t *testing.T) {
	if p := ParseSourceProgress(""); p.Page != "" || p.SeenNewest != "" {
		t.Errorf("空串 = %+v", p)
	}
	if p := ParseSourceProgress("1:0"); p.Page != "" {
		t.Errorf("非 JSON 应忽略: %+v", p)
	}
	raw := SourceProgress{Page: "0:25", Fingerprint: "fp1", SeenNewest: `{"user:1":"2026-08-13T00:00:00Z"}`}.Encode()
	got := ParseSourceProgress(raw)
	if got.Page != "0:25" || got.Fingerprint != "fp1" || got.SeenNewest == "" {
		t.Errorf("JSON 进度 = %+v", got)
	}
}

func TestDecodeWatermarksMap(t *testing.T) {
	ts := "2026-08-13T00:00:00Z"
	enc := EncodeWatermarks(map[string]string{"user:1": ts})
	if !strings.Contains(enc, "user:1") {
		t.Fatalf("encode=%s", enc)
	}
	got := DecodeWatermarks(enc)
	if got["user:1"] != ts {
		t.Fatalf("map roundtrip = %v", got)
	}
	if LookupWatermark(got, "user:1").IsZero() {
		t.Fatal("应有 user:1 水位")
	}
	if !LookupWatermark(got, "user:2").IsZero() {
		t.Fatal("未出现的目标不应有水位")
	}
	if len(DecodeWatermarks(ts)) != 0 {
		t.Fatal("裸时间戳不应解析")
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
