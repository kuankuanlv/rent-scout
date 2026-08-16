package models

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// PriceUnknown 帖子价格默认展示文案（单位元，匹配不到就保持这个）
const PriceUnknown = "暂无"

var (
	priceLabelRe        = regexp.MustCompile(`(?i)(?:月租金?|租金|房租|出租价|报价|定价|价位|价格)\s*[:：是为]?\s*(\d{3,5})`)
	priceYuanMonthRe    = regexp.MustCompile(`(\d{3,5})\s*(?:元|块|rmb|RMB)?\s*[\/／]\s*月`)
	priceMonthYuanRe    = regexp.MustCompile(`(?i)(?:每月|一个月|／月|/月)\s*(\d{3,5})\s*(?:元|块)?`)
	priceYuanPerMonthRe = regexp.MustCompile(`(\d{3,5})\s*(?:元|块).{0,8}?(?:每月|一个月|／月|/月)`)
	priceRentNearYuanRe = regexp.MustCompile(`(?is)(?:租|房租|月租).{0,16}?(\d{3,5})\s*(?:元|块)`)
	priceSymbolRe       = regexp.MustCompile(`(?i)(?:￥|¥|rmb)\s*(\d{3,5})`)
	priceRangeRe        = regexp.MustCompile(`(?:月租|租金|房租|价格|价位)?\s*[:：]?\s*(\d{3,5})\s*[-~～—到至]\s*\d{3,5}`)
	priceKiloRe         = regexp.MustCompile(`(?i)(?:月租|租金|房租|价格|价位)?\s*[:：]?\s*(\d(?:\.\d{1,2})?)\s*[kK](\d)?`)
	priceQianRe         = regexp.MustCompile(`(?:月租|租金|房租|价格|价位)?\s*[:：]?\s*(\d(?:\.\d)?)\s*千\s*(\d)?`)
	priceWanRe          = regexp.MustCompile(`(?:月租|租金|房租|价格|价位)?\s*[:：]?\s*(\d(?:\.\d{1,2})?)\s*万`)
	priceCNRe           = regexp.MustCompile(`(?:月租|租金|房租|价格|价位)\s*[:：]?\s*([一二三四五六七八九十两零〇百千万点]+)\s*(?:元|块)?`)
)

const (
	rentPriceMin = 500
	rentPriceMax = 99999
)

// ExtractRentPrice 多层尽力抠月租数字，只读标题正文，抠不到就暂无
func ExtractRentPrice(title, content string) string {
	text := mapFullwidthDigits(title + "\n" + content)
	for _, fn := range []func(string) string{
		priceFromLabeled,
		priceFromUnits,
		priceFromKiloWan,
		priceFromChinese,
		priceFromRange,
	} {
		if s := fn(text); s != PriceUnknown {
			return s
		}
	}
	return PriceUnknown
}

func priceFromLabeled(text string) string {
	return firstValidPrice(priceLabelRe.FindAllStringSubmatch(text, -1), 1)
}

func priceFromUnits(text string) string {
	for _, re := range []*regexp.Regexp{priceYuanMonthRe, priceMonthYuanRe, priceYuanPerMonthRe, priceRentNearYuanRe, priceSymbolRe} {
		if s := firstValidPrice(re.FindAllStringSubmatch(text, -1), 1); s != PriceUnknown {
			return s
		}
	}
	return PriceUnknown
}

func priceFromKiloWan(text string) string {
	for _, m := range priceKiloRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		n := parseKilo(m[1], "")
		if len(m) >= 3 {
			n = parseKilo(m[1], m[2])
		}
		if s := validRent(n); s != PriceUnknown {
			return s
		}
	}
	for _, m := range priceQianRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		frac := ""
		if len(m) >= 3 {
			frac = m[2]
		}
		n := parseKilo(m[1], frac)
		if s := validRent(n); s != PriceUnknown {
			return s
		}
	}
	for _, m := range priceWanRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		f, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		if s := validRent(int(f*10000 + 0.5)); s != PriceUnknown {
			return s
		}
	}
	return PriceUnknown
}

func priceFromChinese(text string) string {
	for _, m := range priceCNRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		if s := validRent(parseChineseAmount(m[1])); s != PriceUnknown {
			return s
		}
	}
	return PriceUnknown
}

func priceFromRange(text string) string {
	return firstValidPrice(priceRangeRe.FindAllStringSubmatch(text, -1), 1)
}

func firstValidPrice(matches [][]string, idx int) string {
	for _, m := range matches {
		if len(m) <= idx {
			continue
		}
		n, err := strconv.Atoi(m[idx])
		if err != nil {
			continue
		}
		if s := validRent(n); s != PriceUnknown {
			return s
		}
	}
	return PriceUnknown
}

func validRent(n int) string {
	if n < rentPriceMin || n > rentPriceMax {
		return PriceUnknown
	}
	return strconv.Itoa(n)
}

func parseKilo(head, tail string) int {
	f, err := strconv.ParseFloat(head, 64)
	if err != nil {
		return 0
	}
	n := int(f*1000 + 0.5)
	if tail != "" {
		d, err := strconv.Atoi(tail)
		if err == nil {
			n = int(f)*1000 + d*100
		}
	}
	return n
}

var cnNum = map[rune]int{
	'零': 0, '〇': 0,
	'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5,
	'六': 6, '七': 7, '八': 8, '九': 9,
}

func parseChineseAmount(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	total, section, num := 0, 0, 0
	lastUnit := rune(0)
	for _, r := range s {
		if d, ok := cnNum[r]; ok {
			num = d
			continue
		}
		switch r {
		case '十':
			if num == 0 {
				num = 1
			}
			section += num * 10
			num = 0
			lastUnit = '十'
		case '百':
			section += num * 100
			num = 0
			lastUnit = '百'
		case '千':
			section += num * 1000
			num = 0
			lastUnit = '千'
		case '万':
			section += num
			total += section * 10000
			section, num = 0, 0
			lastUnit = '万'
		case '点':
			return 0
		default:
			if unicode.IsSpace(r) {
				continue
			}
			return 0
		}
	}
	if num != 0 {
		switch lastUnit {
		case '千':
			section += num * 100
		case '万':
			section += num * 1000
		default:
			section += num
		}
	}
	return total + section
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

// FillPostPrice 入库前补价格：调用方没填才正则抽，不改标题正文
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
