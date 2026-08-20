package store

import "testing"

func TestParseSourceProgress(t *testing.T) {
	if p := ParseSourceProgress(""); p.Page != "" {
		t.Errorf("空串 = %+v", p)
	}
	if p := ParseSourceProgress("1:0"); p.Page != "" {
		t.Errorf("非 JSON 应忽略: %+v", p)
	}
	raw := SourceProgress{Page: `{"g":0}`, Fingerprint: "fp1"}.Encode()
	got := ParseSourceProgress(raw)
	if got.Page != `{"g":0}` || got.Fingerprint != "fp1" {
		t.Errorf("JSON 进度 = %+v", got)
	}
}

func TestProgressRoundTripAndClear(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	want := SourceProgress{Page: `{"g":0}`, Fingerprint: "r"}
	if err := s.SetProgress("douban", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetProgress("douban")
	if err != nil || !ok {
		t.Fatalf("读进度: ok=%v err=%v", ok, err)
	}
	if got.Page != `{"g":0}` || got.Fingerprint != "r" {
		t.Errorf("进度 = %+v", got)
	}
	if err := s.ClearProgress("douban"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetProgress("douban"); err != nil || ok {
		t.Errorf("清除后 ok=%v err=%v, want 无记录", ok, err)
	}
}
