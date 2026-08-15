package actionref

import (
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	token := Seal(42, "secret")
	if token == "" {
		t.Fatal("seal 空")
	}
	if strings.Contains(token, "42") {
		t.Fatalf("密文不应含 id: %s", token)
	}
	id, err := Open(token, "secret")
	if err != nil || id != 42 {
		t.Fatalf("open id=%d err=%v", id, err)
	}
}

func TestOpenWrongSecret(t *testing.T) {
	token := Seal(7, "a")
	if _, err := Open(token, "b"); err == nil {
		t.Fatal("错密钥应失败")
	}
}

func TestEmptySecretFallbackCompatible(t *testing.T) {
	token := Seal(9, "")
	id, err := Open(token, "")
	if err != nil || id != 9 {
		t.Fatalf("空 secret fallback: id=%d err=%v", id, err)
	}
}
