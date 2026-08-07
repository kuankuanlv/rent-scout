package collector

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

// cookiecloudProvider 从 CookieCloud 拉取并解密 cookie（规格 4.4）。
// 密钥派生与解密：evpBytesToKey + AES-128-CBC（兼容 CookieCloud 标准，
// 迁移参考仓库 spider/cookiecloud.go 已验证逻辑）
type cookiecloudProvider struct {
	cfg config.DoubanCookieConfig
}

func newCookiecloudProvider(cfg config.DoubanCookieConfig) CookieProvider {
	return cookiecloudProvider{cfg: cfg}
}

func (p cookiecloudProvider) Get(ctx context.Context, source string) (string, error) {
	if p.cfg.CookiecloudURL == "" || p.cfg.CookiecloudKey == "" {
		return "", fmt.Errorf("cookiecloud 配置不完整（url/key）")
	}
	// GET {host}/get/{key} 拉取密文
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(p.cfg.CookiecloudURL, "/")+"/get/"+p.cfg.CookiecloudKey, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("拉取 CookieCloud: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var data cookieCloudData
	if err := json.Unmarshal(b, &data); err != nil {
		return "", fmt.Errorf("解析 CookieCloud 响应: %w", err)
	}
	// 解密 → JSON 数组 [{name,value}] → 拼装 cookie 串
	plain, err := decryptCookieCloud(p.cfg.CookiecloudPass, data.Data)
	if err != nil {
		return "", fmt.Errorf("解密 CookieCloud: %w", err)
	}
	return buildCookieString(plain), nil
}

// buildCookieString 解析 CookieCloud 明文 JSON 并拼装 "k=v; k=v" 串
func buildCookieString(plain string) string {
	var items []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(plain), &items); err != nil {
		return strings.TrimSpace(plain) // 非 JSON 兜底：原样返回
	}
	var parts []string
	for _, it := range items {
		if it.Name != "" {
			parts = append(parts, it.Name+"="+it.Value)
		}
	}
	return strings.Join(parts, "; ")
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
