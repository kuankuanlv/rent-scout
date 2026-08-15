package models

import (
	"regexp"
	"strconv"
	"strings"
)

// PriceUnknown 帖子价格默认展示文案（单位元，匹配不到就保持这个）
const PriceUnknown = "暂无"

var rentPricePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)月租[:：\s]*(\d{3,5})`),
	regexp.MustCompile(`(?i)租金[:：\s]*(\d{3,5})`),
	regexp.MustCompile(`(?i)房租[:：\s]*(\d{3,5})`),
	regexp.MustCompile(`(\d{3,5})\s*元\s*[\/／]\s*月`),
	regexp.MustCompile(`(\d{3,5})\s*[/／]\s*月`),
	regexp.MustCompile(`(?i)(?:月租|房租|租金|租)[:：\s]*(\d{3,5})\s*元`),
	regexp.MustCompile(`(?is)(?:租|房租|月租).{0,12}(\d{3,5})\s*元`),
	regexp.MustCompile(`(\d{3,5})\s*元.{0,8}(?:每月|一个月|\/月|／月)`),
	regexp.MustCompile(`(?i)(?:￥|¥|rmb)\s*(\d{3,5})`),
	regexp.MustCompile(`(\d{3,5})\s*[-~～到至]\s*\d{3,5}`),
}

// ExtractRentPrice 从标题和正文里抠月租金（元）。几种写死正则，命中就返回数字字符串，否则暂无。
func ExtractRentPrice(title, content string) string {
	text := title + "\n" + content
	for _, re := range rentPricePatterns {
		m := re.FindStringSubmatch(text)
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 500 || n > 99999 {
			continue
		}
		return strconv.Itoa(n)
	}
	return PriceUnknown
}

// FormatPriceYuan AI 抽出的整数月租；0 表示没识别到
func FormatPriceYuan(n int) string {
	if n <= 0 {
		return PriceUnknown
	}
	return strconv.Itoa(n)
}

// PriceYuan 把库里的价格文案转成整数；暂无或解析失败返回 0
func PriceYuan(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == PriceUnknown {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// FillPostPrice 入库前补价格：调用方没填才正则抽
func FillPostPrice(p *RentPost) {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.Price) != "" && p.Price != PriceUnknown {
		return
	}
	p.Price = ExtractRentPrice(p.Title, p.Content)
}

// FillPostExtracted 入库前补价格和联系方式
func FillPostExtracted(p *RentPost) {
	FillPostPrice(p)
	FillPostContact(p)
}
