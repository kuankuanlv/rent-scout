package config

import "strings"

// CookieMode 豆瓣 cookie 来源；禁止业务里写裸 none/raw/cookiecloud
type CookieMode string

const (
	CookieModeNone        CookieMode = "none"
	CookieModeRaw         CookieMode = "raw"
	CookieModeCookieCloud CookieMode = "cookiecloud"
)

func (m CookieMode) String() string { return string(m) }

func (m CookieMode) Valid() bool {
	switch m {
	case CookieModeNone, CookieModeRaw, CookieModeCookieCloud:
		return true
	}
	return false
}

// ParseCookieMode 空串当 none；未知值原样返回（交给校验报错）
func ParseCookieMode(s string) CookieMode {
	s = strings.ToLower(strings.TrimSpace(s))
	switch CookieMode(s) {
	case "", CookieModeNone:
		return CookieModeNone
	case CookieModeRaw:
		return CookieModeRaw
	case CookieModeCookieCloud:
		return CookieModeCookieCloud
	default:
		return CookieMode(s)
	}
}

// LLMAPIStyle 管理台 AI 提供方
type LLMAPIStyle string

const (
	LLMStyleNone   LLMAPIStyle = "none"
	LLMStyleOpenAI LLMAPIStyle = "openai"
	LLMStyleOther  LLMAPIStyle = "other"
)

func (s LLMAPIStyle) String() string { return string(s) }

func ParseLLMAPIStyle(s string) LLMAPIStyle {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "custom" {
		return LLMStyleOther
	}
	switch LLMAPIStyle(s) {
	case LLMStyleNone, LLMStyleOpenAI, LLMStyleOther:
		return LLMAPIStyle(s)
	default:
		return LLMAPIStyle(s)
	}
}

// 豆瓣 cookie 相关 KV key（syncer / 编解码共用）
const (
	KeyDoubanCookieMode     = "secret.collector.douban.cookie_mode"
	KeyDoubanCookieRaw      = "secret.collector.douban.cookie_raw"
	KeyDoubanCookieCloudURL = "secret.collector.douban.cookiecloud_url"
	KeyDoubanCookieCloudKey = "secret.collector.douban.cookiecloud_key"
	KeyDoubanCookieCloudPwd = "secret.collector.douban.cookiecloud_password"
)
