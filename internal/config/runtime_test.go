package config

import "testing"

func TestHashKVSameContentDifferentOrder(t *testing.T) {
	a := map[string]string{"z": "1", "a": "2", "m": "3"}
	b := map[string]string{"m": "3", "z": "1", "a": "2"}
	if hashKV(a) != hashKV(b) {
		t.Fatalf("同内容不同插入顺序应同 hash: %d vs %d", hashKV(a), hashKV(b))
	}
}

func TestHashKVValueChange(t *testing.T) {
	base := map[string]string{"server.addr": ":7777", "log.level": "info"}
	changed := map[string]string{"server.addr": ":8888", "log.level": "info"}
	if hashKV(base) == hashKV(changed) {
		t.Fatal("改 value 后 hash 应不同")
	}
}
