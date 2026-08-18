package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseImportKVLines(t *testing.T) {
	kv, err := ParseImportKV([]byte(`
# 注释
server.addr=:9090
log.level=debug
collector.douban.groups=https://a.example/x\nhttps://b.example/y
`))
	if err != nil {
		t.Fatal(err)
	}
	if kv["server.addr"] != ":9090" || kv["log.level"] != "debug" {
		t.Fatalf("kv = %v", kv)
	}
	if !strings.Contains(kv["collector.douban.groups"], "https://a.example/x") ||
		!strings.Contains(kv["collector.douban.groups"], "https://b.example/y") {
		t.Fatalf("groups = %q", kv["collector.douban.groups"])
	}
}

func TestParseImportKVJSON(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"server.addr": ":8080", "admin.token": "t"})
	kv, err := ParseImportKV(raw)
	if err != nil {
		t.Fatal(err)
	}
	if kv["server.addr"] != ":8080" || kv["admin.token"] != "t" {
		t.Fatalf("kv = %v", kv)
	}
}

func TestParseImportKVErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"空", ""},
		{"无效行", "nope"},
		{"空 key", "=v"},
		{"JSON 非字符串", `{"x":1}`},
	} {
		if _, err := ParseImportKV([]byte(tc.in)); err == nil {
			t.Errorf("%s 应失败", tc.name)
		}
	}
}
