package collector

import (
	"rent-scout/internal/config/window"
	"strings"
)

// CookieMode 豆瓣 cookie 来源
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

func ParseCookieMode(s string) CookieMode {
	s = strings.ToLower(strings.TrimSpace(s))
	switch CookieMode(s) {
	case "", CookieModeNone:
		return CookieModeNone
	case CookieModeRaw, CookieModeCookieCloud:
		return CookieMode(s)
	default:
		return CookieMode(s)
	}
}

// 各源 cookie 相关 KV key
const (
	KeyDoubanCookieMode     = "secret.collector.douban.cookie_mode"
	KeyDoubanCookieRaw      = "secret.collector.douban.cookie_raw"
	KeyDoubanCookieCloudURL = "secret.collector.douban.cookiecloud_url"
	KeyDoubanCookieCloudKey = "secret.collector.douban.cookiecloud_key"
	KeyDoubanCookieCloudPwd = "secret.collector.douban.cookiecloud_password"

	KeyWeiboCookieMode     = "secret.collector.weibo.cookie_mode"
	KeyWeiboCookieRaw      = "secret.collector.weibo.cookie_raw"
	KeyWeiboCookieRawCN    = "secret.collector.weibo.cookie_raw_cn"
	KeyWeiboCookieCloudURL = "secret.collector.weibo.cookiecloud_url"
	KeyWeiboCookieCloudKey = "secret.collector.weibo.cookiecloud_key"
	KeyWeiboCookieCloudPwd = "secret.collector.weibo.cookiecloud_password"

	KeyXiaohongshuCookieMode     = "secret.collector.xiaohongshu.cookie_mode"
	KeyXiaohongshuCookieRaw      = "secret.collector.xiaohongshu.cookie_raw"
	KeyXiaohongshuCookieCloudURL = "secret.collector.xiaohongshu.cookiecloud_url"
	KeyXiaohongshuCookieCloudKey = "secret.collector.xiaohongshu.cookiecloud_key"
	KeyXiaohongshuCookieCloudPwd = "secret.collector.xiaohongshu.cookiecloud_password"
)

// CookieSource 归一化采集源名
func CookieSource(source string) string {
	s := strings.ToLower(strings.TrimSpace(source))
	if s == "weibo.cn" {
		return "weibo.cn"
	}
	if s == "weibo" {
		return "weibo"
	}
	if s == "xiaohongshu" {
		return "xiaohongshu"
	}
	return "douban"
}

func CookieCloudDomain(source string) string {
	switch CookieSource(source) {
	case "weibo.cn":
		return "weibo.cn"
	case "weibo":
		return "weibo.com"
	case "xiaohongshu":
		return "xiaohongshu.com"
	default:
		return "douban.com"
	}
}

func weiboCookieFamily(source string) bool {
	s := CookieSource(source)
	return s == "weibo" || s == "weibo.cn"
}

func CookieModeKey(source string) string {
	if weiboCookieFamily(source) {
		return KeyWeiboCookieMode
	}
	if CookieSource(source) == "xiaohongshu" {
		return KeyXiaohongshuCookieMode
	}
	return KeyDoubanCookieMode
}

func CookieRawKey(source string) string {
	switch CookieSource(source) {
	case "weibo.cn":
		return KeyWeiboCookieRawCN
	case "weibo":
		return KeyWeiboCookieRaw
	case "xiaohongshu":
		return KeyXiaohongshuCookieRaw
	default:
		return KeyDoubanCookieRaw
	}
}

func CookieCloudURLKey(source string) string {
	if weiboCookieFamily(source) {
		return KeyWeiboCookieCloudURL
	}
	if CookieSource(source) == "xiaohongshu" {
		return KeyXiaohongshuCookieCloudURL
	}
	return KeyDoubanCookieCloudURL
}

func CookieCloudKeyKey(source string) string {
	if weiboCookieFamily(source) {
		return KeyWeiboCookieCloudKey
	}
	if CookieSource(source) == "xiaohongshu" {
		return KeyXiaohongshuCookieCloudKey
	}
	return KeyDoubanCookieCloudKey
}

func CookieCloudPwdKey(source string) string {
	if weiboCookieFamily(source) {
		return KeyWeiboCookieCloudPwd
	}
	if CookieSource(source) == "xiaohongshu" {
		return KeyXiaohongshuCookieCloudPwd
	}
	return KeyDoubanCookieCloudPwd
}

type Config struct {
	Sources     []string
	Interval    int          // 全局默认采集间隔（秒）
	JitterRatio float64      // 间隔随机抖动比例
	MaxAgeDays  int          // 时间窗：超过此天数的帖子不再采集
	Douban      DoubanConfig // 豆瓣源
	Weibo       WeiboConfig
	Xiaohongshu XiaohongshuConfig
}

type DoubanConfig struct {
	Groups    []string
	Interval  int
	RangeFrom string
	RangeTo   string
}

type WeiboConfig struct {
	Users       []string
	SuperTopics []string
	Interval    int
	RangeFrom   string
}

type XiaohongshuConfig struct {
	Searches  []string
	Topics    []string
	Users     []string
	Interval  int
	RangeFrom string
	MaxPages  int
}

type DoubanCookieConfig struct {
	CookieMode      string
	CookieRaw       string
	CookieRawCN     string
	CookiecloudURL  string
	CookiecloudKey  string
	CookiecloudPass string
}

type SecretsCollector struct {
	Douban      DoubanCookieConfig
	Weibo       DoubanCookieConfig
	Xiaohongshu DoubanCookieConfig
}

func (c SecretsCollector) CookieFor(source string) DoubanCookieConfig {
	switch CookieSource(source) {
	case "weibo.cn":
		dc := c.Weibo
		dc.CookieRaw = dc.CookieRawCN
		return dc
	case "weibo":
		return c.Weibo
	case "xiaohongshu":
		return c.Xiaohongshu
	default:
		return c.Douban
	}
}

var DefaultDoubanGroups = []string{
	"#北京租房（35417）",
	"https://www.douban.com/group/35417/discussion",
	"#北京无中介租房（262626）",
	"https://www.douban.com/group/262626/discussion",
	"#北京租房（232413）",
	"https://www.douban.com/group/232413/discussion",
	"#北京租房联盟（338147）",
	"https://www.douban.com/group/338147/discussion",
	"#北京租房（331294）",
	"https://www.douban.com/group/331294/discussion",
	"#北京租房大全（550436）",
	"https://www.douban.com/group/550436/discussion",
	"#北京租房（596202）",
	"https://www.douban.com/group/596202/discussion",
}

func ApplyDefaults(c *Config) {
	if c.Interval == 0 {
		c.Interval = 300
	}
	if c.JitterRatio == 0 {
		c.JitterRatio = 0.2
	}
	if c.MaxAgeDays == 0 {
		c.MaxAgeDays = 7
	}
	if len(c.Douban.Groups) == 0 {
		c.Douban.Groups = DefaultDoubanGroups
	}
	if c.Douban.Interval == 0 {
		c.Douban.Interval = 3
	}
	if c.Weibo.Interval == 0 {
		c.Weibo.Interval = 5
	}

	if c.Xiaohongshu.Interval == 0 {
		c.Xiaohongshu.Interval = 600
	}
	if c.Xiaohongshu.RangeFrom == "" {
		c.Xiaohongshu.RangeFrom = "-10"
	}
	c.Xiaohongshu.RangeFrom = window.CanonicalDayOffset(c.Xiaohongshu.RangeFrom)
	if c.Xiaohongshu.MaxPages == 0 {
		c.Xiaohongshu.MaxPages = 5
	}

	if c.Weibo.RangeFrom == "" {
		c.Weibo.RangeFrom = "-10"
	}
	c.Weibo.RangeFrom = window.CanonicalDayOffset(c.Weibo.RangeFrom)

	if c.Douban.RangeFrom == "" {
		c.Douban.RangeFrom = "-10"
	}
	c.Douban.RangeFrom = window.CanonicalDayOffset(c.Douban.RangeFrom)

	if c.Douban.RangeTo == "" {
		c.Douban.RangeTo = "now"
	}
	c.Douban.RangeTo = window.CanonicalDayOffset(c.Douban.RangeTo)
}

func (c Config) SourceInterval(source string) int {
	_ = source
	if c.Interval > 0 {
		return c.Interval
	}
	return 300
}
