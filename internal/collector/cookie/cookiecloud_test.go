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

	got, err := InspectCookieCloud(context.Background(), config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  key,
		CookiecloudPass: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Cookie, "cookie-value-123") {
		t.Errorf("解密结果缺少 cookie 值: %q", got.Cookie)
	}
}

// 官方 CookieCloud：响应字段 encrypted，口令 md5(uuid-password)[:16]，Salted__ + AES-256
func TestCookiecloudOfficialEncrypted(t *testing.T) {
	const (
		uuid     = "0123456789abcdef0123456789abcdef"
		password = "test-pass"
	)
	plain := `{"cookie_data":{"douban.com":[{"name":"dbcl2","value":"official-cookie","domain":".douban.com"}]}}`
	payload := `{"encrypted":"` + encryptOfficialForTest(t, uuid, password, plain) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("官方路径应先 GET, got %s", r.Method)
		}
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	got, err := InspectCookieCloud(context.Background(), config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  uuid,
		CookiecloudPass: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.CipherField != "encrypted" {
		t.Errorf("cipher_field = %q", got.CipherField)
	}
	if got.Algo != "official-legacy-aes256" {
		t.Errorf("algo = %q", got.Algo)
	}
	if !strings.Contains(got.Cookie, "official-cookie") {
		t.Errorf("解密结果缺少 cookie 值: %+v", got)
	}
}

func TestCookiecloudServerPlainCookieData(t *testing.T) {
	payload := `{"cookie_data":{"www.douban.com":[{"name":"ck","value":"plain-ck"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	got, err := InspectCookieCloud(context.Background(), config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  "any-uuid",
		CookiecloudPass: "any",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Algo != "server-plain" || !strings.Contains(got.Cookie, "plain-ck") {
		t.Errorf("got algo=%s cookie=%q", got.Algo, got.Cookie)
	}
}

// gongji cookiecloud.rs 已知向量：key=md5("test-uuid-test-pass")[:16]
func TestDecryptGongjiAES128Vectors(t *testing.T) {
	const (
		uuid         = "test-uuid"
		password     = "test-pass"
		wantKey      = "e0c4589a68c8b0ec"
		fixedCipher  = "9iHUf3pYL2A+yk44DYPWTQ=="                     // {"a":1}
		legacyCipher = "U2FsdGVkX186By4kIqAOkpGyeWuhFWr2/ebEkKaydDg=" // {"b":2}
	)
	if got := string(officialPassphrase(uuid, password)); got != wantKey {
		t.Fatalf("the_key = %q, want %q", got, wantKey)
	}
	plain, algo, err := decryptCookieCloud(uuid, password, fixedCipher, "")
	if err != nil {
		t.Fatal(err)
	}
	if algo != "official-fixed-aes128" || plain != `{"a":1}` {
		t.Fatalf("fixed algo=%s plain=%q", algo, plain)
	}
	plain, algo, err = decryptCookieCloud(uuid, password, legacyCipher, "")
	if err != nil {
		t.Fatal(err)
	}
	if algo != "official-legacy-aes256" || plain != `{"b":2}` {
		t.Fatalf("legacy algo=%s plain=%q", algo, plain)
	}
	if _, _, err = decryptCookieCloud(uuid, password, legacyCipher, "aes-128-cbc-fixed"); err == nil {
		t.Fatal("声明 fixed 的 legacy 密文不应成功")
	}
}

func TestCookiecloudOfficialFixedAES128(t *testing.T) {
	const uuid, password = "test-uuid", "test-pass"
	plain := `{"cookie_data":{"douban.com":[{"name":"dbcl2","value":"aes128-cookie","domain":".douban.com"}]}}`
	enc := encryptFixedAES128ForTest(t, uuid, password, plain)
	payload := `{"encrypted":"` + enc + `","crypto_type":"aes-128-cbc-fixed"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	got, err := InspectCookieCloud(context.Background(), config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  uuid,
		CookiecloudPass: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Algo != "official-fixed-aes128" || !strings.Contains(got.Cookie, "aes128-cookie") {
		t.Fatalf("algo=%s cookie=%q", got.Algo, got.Cookie)
	}
}

func TestCookiecloudEmptyPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"foo":1}`))
	}))
	defer srv.Close()

	_, err := InspectCookieCloud(context.Background(), config.DoubanCookieConfig{
		CookiecloudURL:  srv.URL,
		CookiecloudKey:  "uuid",
		CookiecloudPass: "pass",
	})
	if err == nil || !strings.Contains(err.Error(), "无 encrypted") {
		t.Fatalf("应报无密文字段, err=%v", err)
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
	prev := ListDoubanCookiePreviews(wrapped)
	if len(prev) != 1 || prev[0] != "ck=1" {
		t.Errorf("ListDoubanCookiePreviews = %v", prev)
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
	spaced := `抱歉，您好像不是。。人。。类，请稍后再试`
	if !RiskDetected(spaced) {
		t.Fatal("间隔符「非人类」应变风控")
	}
	anon := `<p>有异常请求从你的 IP 发出，请 <a href="/login">登录</a> 使用豆瓣</p>`
	if !RiskDetected(anon) {
		t.Fatal("无 cookie 豆瓣登录提示应变风控")
	}
	snip = RiskSnippet(anon)
	if !strings.Contains(snip, "有异常请求从你的 IP 发出") || !strings.Contains(snip, "登录") {
		t.Errorf("应抠出页面原文, snippet=%q", snip)
	}
}

// encryptFixedAES128ForTest 官方 aes-128-cbc-fixed：key 为 16 位 hex 口令，IV 全 0
func encryptFixedAES128ForTest(t *testing.T, uuid, password, plain string) string {
	t.Helper()
	key := officialPassphrase(uuid, password)
	if len(key) != 16 {
		t.Fatalf("key len=%d", len(key))
	}
	padded := pkcs7Pad([]byte(plain), aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(ct, padded)
	return base64.StdEncoding.EncodeToString(ct)
}

// encryptOfficialForTest 官方 legacy：md5(uuid-password)[:16] 作口令，EVP 产出 32+16，AES-256-CBC
func encryptOfficialForTest(t *testing.T, uuid, password, plain string) string {
	t.Helper()
	pass := officialPassphrase(uuid, password)
	salt := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	keyIv := evpBytesToKey(pass, salt, 48)
	block, err := aes.NewCipher(keyIv[:32])
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad([]byte(plain), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, keyIv[32:]).CryptBlocks(ct, padded)
	out := append([]byte("Salted__"), salt...)
	out = append(out, ct...)
	return base64.StdEncoding.EncodeToString(out)
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
