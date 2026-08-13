package cookie

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rent-scout/internal/config"
)

// cookiecloud 模式：GET {host}/get/{key} 拉取加密 cookie 并解密（AES-128-CBC，legacy Salted__ 格式）
func TestCookiecloudProvider(t *testing.T) {
	const (
		key      = "0123456789abcdef0123456789abcdef" // 同时充当 path 标识（CookiecloudKey）
		password = "test-pass"
	)
	plain := `[{"name":"dbcl2","value":"cookie-value-123"}]`
	encrypted := encryptForTest(t, password, plain) // legacy（Salted__）格式密文，与解密互逆

	payload := `{"data":"` + base64.StdEncoding.EncodeToString(encrypted) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, key) {
			t.Errorf("请求路径错误: %s", r.URL.Path)
		}
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	p, err := New("cookiecloud", config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  key,
		CookiecloudPass: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "douban")
	if err != nil {
		t.Fatal(err)
	}
		if !strings.Contains(got, "cookie-value-123") {
			t.Errorf("解密结果缺少 cookie 值: %q", got)
		}
	}

	func TestBuildCookieStringDoubanDomainFilter(t *testing.T) {
		plain := `[
			{"domain":".douban.com","name":"dbcl2","value":"yes"},
			{"domain":".weibo.com","name":"SUB","value":"no"},
			{"name":"bid","value":"legacy-no-domain"}
		]`
		got := buildCookieString(plain)
		if !strings.Contains(got, "dbcl2=yes") || !strings.Contains(got, "bid=legacy-no-domain") {
			t.Errorf("应保留豆瓣/无 domain: %q", got)
		}
		if strings.Contains(got, "SUB=") {
			t.Errorf("不应含微博 cookie: %q", got)
		}
		names := ListDoubanCookieNames(plain)
		if len(names) != 2 {
			t.Fatalf("names=%v", names)
		}

		wrapped := `{"cookie_data":{"douban.com":[{"name":"ck","value":"1"}],"weibo.com":[{"name":"w","value":"2"}]}}`
		got = buildCookieString(wrapped)
		if got != "ck=1" {
			t.Errorf("cookie_data 过滤 = %q", got)
		}
		if n := ListDoubanCookieNames(wrapped); len(n) != 1 || n[0] != "ck" {
			t.Errorf("ListDoubanCookieNames = %v", n)
		}
	}

	func TestRiskSnippet(t *testing.T) {
		body := `<html><body>抱歉，检测到有异常请求，请稍后再试</body></html>`
		if !RiskDetected(body) {
			t.Fatal("应判定风控")
		}
		snip := RiskSnippet(body)
		if !strings.Contains(snip, "异常请求") {
			t.Errorf("snippet=%q", snip)
		}
		if RiskSnippet("正常页面") != "" {
			t.Error("正常页不应有 snippet")
		}
	}

// encryptForTest 测试辅助：构造 legacy（Salted__ + salt + AES-128-CBC）格式密文，
// 与 decryptCookieCloud 的 Salted__ 分支互逆（密钥派生同为 evpBytesToKey(password, salt, 32)）
func encryptForTest(t *testing.T, password, plain string) []byte {
	t.Helper()
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	keyIv := evpBytesToKey([]byte(password), salt, 32)
	block, err := aes.NewCipher(keyIv[:16])
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad([]byte(plain), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, keyIv[16:]).CryptBlocks(ct, padded)
	out := append([]byte("Salted__"), salt...)
	return append(out, ct...)
}

// pkcs7Pad 测试辅助：PKCS7 填充
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}
