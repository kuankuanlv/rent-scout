package cookie

import (
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

	"rent-scout/internal/config"
)

// cookieCloudData CookieCloud 服务响应（迁移参考仓库结构）
type cookieCloudData struct {
	Data string `json:"data"`
}

// cookieItem CookieCloud 明文里的单条 cookie
type cookieItem struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

// cookiecloudProvider 从 CookieCloud 拉取并解密 cookie（规格 4.4）。
// 密钥派生与解密：evpBytesToKey + AES-128-CBC（兼容 CookieCloud 标准，
// 迁移参考仓库 spider/cookiecloud.go 已验证逻辑）
type cookiecloudProvider struct {
	cfg config.DoubanCookieConfig
}

func newCookiecloudProvider(cfg config.DoubanCookieConfig) Provider {
	return cookiecloudProvider{cfg: cfg}
}

func (p cookiecloudProvider) Get(ctx context.Context, source string) (string, error) {
	ck, _, err := InspectCookieCloud(ctx, p.cfg)
	return ck, err
}

// InspectCookieCloud 拉取并解密 CookieCloud：返回豆瓣 cookie 串 + 命中名称（探测用，无明文 value）
func InspectCookieCloud(ctx context.Context, cfg config.DoubanCookieConfig) (cookieStr string, names []string, err error) {
	if cfg.CookiecloudURL == "" || cfg.CookiecloudKey == "" {
		return "", nil, fmt.Errorf("cookiecloud 配置不完整（url/key）")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(cfg.CookiecloudURL, "/")+"/get/"+cfg.CookiecloudKey, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("拉取 CookieCloud: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	var data cookieCloudData
	if err := json.Unmarshal(b, &data); err != nil {
		return "", nil, fmt.Errorf("解析 CookieCloud 响应: %w", err)
	}
	plain, err := decryptCookieCloud(cfg.CookiecloudPass, data.Data)
	if err != nil {
		return "", nil, fmt.Errorf("解密 CookieCloud: %w", err)
	}
	return buildCookieString(plain), ListDoubanCookieNames(plain), nil
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
	return strings.Contains(d, "douban.com") || strings.Contains(d, "douban")
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
		if domain == "cookie_data" || domain == "update_time" || domain == "data" {
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
func decryptCookieCloud(password, encrypted string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	keyIv := evpBytesToKey([]byte(password), nil, 32)
	aesKey := keyIv[:16]
	aesIv := keyIv[16:]
	if len(raw) >= 16 && string(raw[:8]) == "Salted__" {
		// legacy 格式：Salted__ + salt + 密文，密钥派生带 salt
		salt := raw[8:16]
		keyIv = evpBytesToKey([]byte(password), salt, 32)
		aesKey = keyIv[:16]
		aesIv = keyIv[16:]
		raw = raw[16:]
	}
	if len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("密文长度非法")
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, aesIv).CryptBlocks(plain, raw)
	return string(pkcs7Unpad(plain)), nil
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
func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	n := int(data[len(data)-1])
	if n > len(data) || n == 0 {
		return data
	}
	return data[:len(data)-n]
}
