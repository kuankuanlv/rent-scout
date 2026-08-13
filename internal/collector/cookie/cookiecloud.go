package cookie

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
)

var cookieCloudHTTP = &http.Client{Timeout: 10 * time.Second}

const (
	fieldCookieData = "cookie_data"
	fieldEncrypted  = "encrypted"
	fieldData       = "data"
	// InterestDomain CookieCloud 应答里只关心这个域
	InterestDomain = "douban.com"
)

type cookieCloudData struct {
	Data       string          `json:"data"`
	Encrypted  string          `json:"encrypted"`
	CryptoType string          `json:"crypto_type"`
	CookieData json.RawMessage `json:"cookie_data"`
}

type cookieItem struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

// CloudInspect CookieCloud 拉取解密结果（探测用；Previews 已脱敏）
type CloudInspect struct {
	Cookie      string
	Names       []string
	Previews    []string // name=脱敏 value
	Algo        string
	CipherField string
	HTTPStatus  int
	Domains     []string
}

// InspectCookieCloud 拉取并解密 CookieCloud：只拼豆瓣域 cookie；探测/同步共用
func InspectCookieCloud(ctx context.Context, cfg config.DoubanCookieConfig) (CloudInspect, error) {
	log := pkglog.Component(pkglog.DoubanCookieCloud)
	var empty CloudInspect
	if cfg.CookiecloudURL == "" || cfg.CookiecloudKey == "" {
		err := fmt.Errorf("cookiecloud 配置不完整（url/key）")
		log.Error("请求失败", "err", err)
		return empty, err
	}
	status, raw, err := fetchCookieCloud(ctx, cfg)
	empty.HTTPStatus = status
	if err != nil {
		return empty, err
	}
	data, field, cipher, err := pickCookieCloudCipher(raw)
	empty.CipherField = field
	if err != nil {
		log.Error("应答异常", "err", err)
		return empty, err
	}
	log.Info("应答", "http", status, "bytes", len(raw), "field", field)

	var plain, algo string
	if field == fieldCookieData {
		plain = string(raw)
		algo = "server-plain"
	} else {
		plain, algo, err = decryptCookieCloud(cfg.CookiecloudKey, cfg.CookiecloudPass, cipher, data.CryptoType)
		if err != nil {
			log.Error("解密失败", "field", field, "err", err)
			return empty, fmt.Errorf("解密 CookieCloud: %w", err)
		}
		log.Info("解密", "algo", algo, "field", field)
	}

	hit, skip := filterInterestDomains(plain)
	log.Info("过滤域名", "keep", InterestDomain, "hit", hit, "skip_n", skip)
	ins := CloudInspect{
		Cookie:      buildCookieString(plain),
		Names:       ListDoubanCookieNames(plain),
		Previews:    ListDoubanCookiePreviews(plain),
		Algo:        algo,
		CipherField: field,
		HTTPStatus:  status,
		Domains:     hit,
	}
	if ins.Cookie == "" {
		err := fmt.Errorf("未拿到 %s cookie（algo=%s 域=%s）", InterestDomain, ins.Algo, strings.Join(hit, ","))
		log.Error("未拿到 cookie", "err", err)
		return ins, err
	}
	log.Info("拿到 cookie", "names", ins.Names, "count", len(ins.Names))
	return ins, nil
}

func fetchCookieCloud(ctx context.Context, cfg config.DoubanCookieConfig) (int, []byte, error) {
	getURL := strings.TrimSuffix(cfg.CookiecloudURL, "/") + "/get/" + cfg.CookiecloudKey
	status, body, err := doCookieCloud(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return status, nil, err
	}
	if cookieCloudHasPayload(body) {
		return status, body, nil
	}
	payload, _ := json.Marshal(map[string]string{
		"uuid":     cfg.CookiecloudKey,
		"password": cfg.CookiecloudPass,
	})
	postStatus, postBody, postErr := doCookieCloud(ctx, http.MethodPost, getURL, payload)
	if postErr != nil {
		return status, body, fmt.Errorf("CookieCloud GET 无密文字段且 POST 失败: %w", postErr)
	}
	return postStatus, postBody, nil
}

func doCookieCloud(ctx context.Context, method, rawURL string, payload []byte) (int, []byte, error) {
	log := pkglog.Component(pkglog.DoubanCookieCloud)
	log.Info("请求", "method", method, "url", rawURL)
	var rdr io.Reader
	if payload != nil {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		log.Error("请求失败", "err", err)
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cookieCloudHTTP.Do(req)
	if err != nil {
		log.Error("请求失败", "err", err)
		return 0, nil, fmt.Errorf("拉取 CookieCloud: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		log.Error("应答读取失败", "http", resp.StatusCode, "err", err)
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("CookieCloud HTTP %d body=%s", resp.StatusCode, clipLog(string(b), 300))
		log.Error("应答异常", "http", resp.StatusCode, "err", err)
		return resp.StatusCode, b, err
	}
	return resp.StatusCode, b, nil
}

func cookieCloudHasPayload(b []byte) bool {
	_, field, cipher, err := pickCookieCloudCipher(b)
	if err != nil {
		return false
	}
	return field == fieldCookieData || strings.TrimSpace(cipher) != ""
}

func pickCookieCloudCipher(b []byte) (cookieCloudData, string, string, error) {
	var data cookieCloudData
	if err := json.Unmarshal(b, &data); err != nil {
		return data, "", "", fmt.Errorf("解析 CookieCloud 响应: %w raw=%s", err, clipLog(string(b), 300))
	}
	if len(data.CookieData) > 0 && string(data.CookieData) != "null" {
		return data, fieldCookieData, "", nil
	}
	if s := strings.TrimSpace(data.Encrypted); s != "" {
		return data, fieldEncrypted, s, nil
	}
	if s := strings.TrimSpace(data.Data); s != "" {
		return data, fieldData, s, nil
	}
	return data, "", "", fmt.Errorf("CookieCloud 响应无 encrypted/data/cookie_data raw=%s", clipLog(string(b), 300))
}

func redactCookieCloudJSON(b []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return []byte("(非 JSON)")
	}
	if _, ok := m["password"]; ok {
		m["password"] = "***"
	}
	out, err := json.Marshal(m)
	if err != nil {
		return []byte("(脱敏失败)")
	}
	return out
}

func clipLog(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func filterInterestDomains(plain string) (hit []string, skipN int) {
	items := parseCookieCloudItems(plain)
	if items == nil {
		return nil, 0
	}
	seenHit := map[string]bool{}
	seenSkip := map[string]bool{}
	for _, it := range items {
		d := strings.TrimSpace(it.Domain)
		if d == "" {
			d = "(空)"
		}
		if isDoubanCookieDomain(it.Domain) {
			if !seenHit[d] {
				seenHit[d] = true
				hit = append(hit, d)
			}
			continue
		}
		if !seenSkip[d] {
			seenSkip[d] = true
			skipN++
		}
	}
	return hit, skipN
}

func listCookieCloudDomains(plain string) []string {
	hit, _ := filterInterestDomains(plain)
	return hit
}

// buildCookieString 解析 CookieCloud 明文 JSON，只保留豆瓣相关域名，拼装 "k=v; k=v"
func buildCookieString(plain string) string {
	items := parseCookieCloudItems(plain)
	if items == nil {
		return strings.TrimSpace(plain) // 非 JSON 兜底：原样返回
	}
	var parts []string
	for _, it := range items {
		if it.Name == "" || !isDoubanCookieDomain(it.Domain) {
			continue
		}
		parts = append(parts, it.Name+"="+it.Value)
	}
	return strings.Join(parts, "; ")
}

// ListDoubanCookieNames 列出 CookieCloud 明文中命中豆瓣域名的 cookie 名（探测 API 用，无明文 value）
func ListDoubanCookieNames(plain string) []string {
	items := parseCookieCloudItems(plain)
	if items == nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Name == "" || !isDoubanCookieDomain(it.Domain) || seen[it.Name] {
			continue
		}
		seen[it.Name] = true
		names = append(names, it.Name)
	}
	return names
}

// ListDoubanCookiePreviews 脱敏预览 name=value…（探测摘要用）
func ListDoubanCookiePreviews(plain string) []string {
	items := parseCookieCloudItems(plain)
	if items == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Name == "" || !isDoubanCookieDomain(it.Domain) || seen[it.Name] {
			continue
		}
		seen[it.Name] = true
		out = append(out, it.Name+"="+MaskCookiePreview(it.Value))
	}
	return out
}

// isDoubanCookieDomain domain 含 douban.com / douban；空 domain 视为兼容旧扁平数组（全收）
func isDoubanCookieDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "" {
		return true
	}
	return strings.Contains(d, InterestDomain) || strings.Contains(d, "douban")
}

// parseCookieCloudItems 兼容：[{name,value,domain}] / {cookie_data:{domain:[...]}} / {域名:[...]}
func parseCookieCloudItems(plain string) []cookieItem {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return nil
	}
	var arr []cookieItem
	if err := json.Unmarshal([]byte(plain), &arr); err == nil {
		return arr
	}
	// CookieCloud 常见：{"cookie_data":{"douban.com":[...]}}
	var wrap struct {
		CookieData map[string][]cookieItem `json:"cookie_data"`
	}
	if err := json.Unmarshal([]byte(plain), &wrap); err == nil && wrap.CookieData != nil {
		var out []cookieItem
		for domain, list := range wrap.CookieData {
			for _, it := range list {
				if it.Domain == "" {
					it.Domain = domain
				}
				out = append(out, it)
			}
		}
		return out
	}
	// 兜底：顶层就是 domain → []cookie
	var byDomain map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plain), &byDomain); err != nil {
		return nil
	}
	var out []cookieItem
	for domain, raw := range byDomain {
		if domain == "cookie_data" || domain == "update_time" || domain == "data" || domain == "local_storage_data" || domain == "encrypted" {
			continue
		}
		var list []cookieItem
		if err := json.Unmarshal(raw, &list); err != nil {
			continue
		}
		for _, it := range list {
			if it.Domain == "" {
				it.Domain = domain
			}
			out = append(out, it)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decryptCookieCloud 解密 CookieCloud 密文：兼容 aes-128-cbc-fixed 与 legacy 两种格式
// （迁移参考仓库 decryptAES128CBC + decryptLegacy）
// 对齐 gongji：key=md5(uuid-password)[:16]；先 AES-128-CBC 零 IV，失败再 Salted__ AES-256；声明 fixed 不回退
func decryptCookieCloud(uuid, password, encrypted, cryptoType string) (string, string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return "", "", fmt.Errorf("密文不是合法 base64: %w", err)
	}
	official := officialPassphrase(uuid, password)
	type attempt struct {
		name string
		fn   func() (string, error)
	}
	if strings.EqualFold(strings.TrimSpace(cryptoType), "aes-128-cbc-fixed") {
		plain, err := decryptFixedAES128(raw, official)
		if err != nil {
			return "", "", fmt.Errorf("aes-128-cbc-fixed: %w", err)
		}
		if !plausibleCookiePlain(plain) {
			return "", "", fmt.Errorf("aes-128-cbc-fixed: 明文不像 cookie JSON")
		}
		return plain, "official-fixed-aes128", nil
	}
	attempts := []attempt{
		{"official-fixed-aes128", func() (string, error) { return decryptFixedAES128(raw, official) }},
		{"official-legacy-aes256", func() (string, error) { return decryptSalted(raw, official, 32) }},
		{"spider-legacy-aes128", func() (string, error) { return decryptSalted(raw, []byte(password), 16) }},
		{"spider-unsalted-aes128", func() (string, error) { return decryptUnsaltedEVP(raw, []byte(password)) }},
	}
	var tried []string
	for _, a := range attempts {
		plain, err := a.fn()
		if err != nil {
			tried = append(tried, a.name+"="+err.Error())
			continue
		}
		if !plausibleCookiePlain(plain) {
			tried = append(tried, a.name+"=明文不像 cookie JSON")
			continue
		}
		return plain, a.name, nil
	}
	return "", "", fmt.Errorf("全部算法失败: %s", strings.Join(tried, "; "))
}

func officialPassphrase(uuid, password string) []byte {
	sum := md5.Sum([]byte(uuid + "-" + password))
	hex := fmt.Sprintf("%x", sum)
	return []byte(hex[:16])
}

func decryptSalted(raw, passphrase []byte, keyLen int) (string, error) {
	if len(raw) < 16 || string(raw[:8]) != "Salted__" {
		return "", fmt.Errorf("非 Salted__ 格式")
	}
	salt := raw[8:16]
	ct := raw[16:]
	keyIv := evpBytesToKey(passphrase, salt, keyLen+aes.BlockSize)
	return decryptCBC(ct, keyIv[:keyLen], keyIv[keyLen:keyLen+aes.BlockSize])
}

func decryptFixedAES128(raw, key []byte) (string, error) {
	if len(raw) >= 8 && string(raw[:8]) == "Salted__" {
		return "", fmt.Errorf("fixed 不含 Salted__")
	}
	if len(key) < 16 {
		return "", fmt.Errorf("密钥过短")
	}
	return decryptCBC(raw, key[:16], make([]byte, aes.BlockSize))
}

func decryptUnsaltedEVP(raw, password []byte) (string, error) {
	if len(raw) >= 8 && string(raw[:8]) == "Salted__" {
		return "", fmt.Errorf("跳过 Salted__")
	}
	keyIv := evpBytesToKey(password, nil, 32)
	return decryptCBC(raw, keyIv[:16], keyIv[16:])
}

func decryptCBC(raw, key, iv []byte) (string, error) {
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("密文长度非法")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw)
	out, err := pkcs7Unpad(plain)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(out) {
		return "", fmt.Errorf("明文非 UTF-8")
	}
	return string(out), nil
}

func plausibleCookiePlain(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if s[0] == '{' || s[0] == '[' {
		return json.Valid([]byte(s))
	}
	return strings.Contains(s, "=")
}

// evpBytesToKey OpenSSL EVP_BytesToKey 密钥派生（MD5 迭代；迁移参考仓库）
func evpBytesToKey(password, salt []byte, output int) []byte {
	var prev, result []byte
	for len(result) < output {
		h := md5.Sum(append(append(prev, password...), salt...))
		result = append(result, h[:]...)
		prev = h[:]
	}
	return result[:output]
}

// pkcs7Unpad 移除 PKCS7 填充（迁移参考仓库）
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("空明文")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > len(data) || n > aes.BlockSize {
		return nil, fmt.Errorf("填充非法")
	}
	for i := 0; i < n; i++ {
		if data[len(data)-1-i] != byte(n) {
			return nil, fmt.Errorf("填充非法")
		}
	}
	return data[:len(data)-n], nil
}
