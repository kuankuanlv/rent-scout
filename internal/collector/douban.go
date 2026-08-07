package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"rent-scout/internal/models"
)

// 编译断言：Douban 满足 Source 接口
var _ Source = (*Douban)(nil)

// DoubanOptions douban 适配器参数
type DoubanOptions struct {
	GroupURLs []string       // 小组讨论列表 URL（config [collector.douban].groups）
	Cookie    CookieProvider // cookie 来源（可为 none）
	Client    *http.Client   // HTTP 客户端（测试注入 httptest 客户端）
}

// Douban 豆瓣小组适配器（规格 4.3）：List 列表页 → Detail 详情页
type Douban struct {
	groupURLs []string
	cookie    CookieProvider
	client    *http.Client
}

// NewDouban 创建适配器；至少一个小组 URL
func NewDouban(opts DoubanOptions) (*Douban, error) {
	if len(opts.GroupURLs) == 0 {
		return nil, fmt.Errorf("douban 需要至少一个小组 URL")
	}
	if opts.Cookie == nil {
		opts.Cookie = noneProvider{}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Douban{groupURLs: opts.GroupURLs, cookie: opts.Cookie, client: opts.Client}, nil
}

func (d *Douban) Name() string { return "douban" }

// Detail 抓取详情页并归一化为 RentPost（只对未存在的新帖调用）。
// 正文 = 首帖正文 HTML（含图片链接，不含评论）；Raw = 原始 HTML（规格 3.1）
func (d *Douban) Detail(ctx context.Context, item ListItem) (models.RentPost, error) {
	body, err := d.get(ctx, item.URL)
	if err != nil {
		return models.RentPost{}, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return models.RentPost{}, fmt.Errorf("解析详情页: %w", err)
	}
	// 正文：.topic-content 的 HTML（保留 <img> 图片链接）
	content, _ := doc.Find(".topic-content").First().Html()
	// 标题兜底：详情页 h1（列表标题优先）
	title := item.Title
	if title == "" {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	return models.RentPost{
		Source:      d.Name(),
		ExternalID:  item.ExternalID,
		URL:         item.URL,
		Title:       title,
		Content:     strings.TrimSpace(content),
		Author:      item.Author,
		PublishedAt: item.PublishedAt,
		Status:      models.PostStatusCollected,
		Raw:         body,
	}, nil
}

// riskDetected 风控检测：响应体含异常关键字即触发（参考仓库 detail.go）
func riskDetected(body string) bool {
	for _, kw := range []string{"检测到有异常请求", "禁止访问", "turing.captcha", "有异常请求"} {
		if strings.Contains(body, kw) {
			return true
		}
	}
	return false
}

// List 抓取一页讨论列表。cursor 格式 "组下标:偏移"（如 "0:25"；"" = 从第一组第一页）。
// 当前组该页有条目 → 同组下一页 "gi:offset+25"；空页 → 推进到下一组 "gi+1:0"；
// 最后一组结束或下标越界 → ""（无更多页）。多小组按配置 groups 轮转
func (d *Douban) List(ctx context.Context, cursor string) ([]ListItem, string, error) {
	gi, offset := parseListCursor(cursor)
	if gi >= len(d.groupURLs) {
		return nil, "", nil // 已遍历全部小组：结束
	}
	// 组 URL 拼接 start 参数（豆瓣分页 start=0,25,50...）
	u, err := url.Parse(d.groupURLs[gi])
	if err != nil {
		return nil, "", fmt.Errorf("小组 URL 非法: %w", err)
	}
	q := u.Query()
	q.Set("start", strconv.Itoa(offset))
	u.RawQuery = q.Encode()

	body, err := d.get(ctx, u.String())
	if err != nil {
		return nil, "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("解析列表页: %w", err)
	}

	var items []ListItem
	// 行解析：跳过表头行（参考仓库 service.go:82-120）
	doc.Find("table.olt tr").Each(func(i int, sel *goquery.Selection) {
		if i == 0 {
			return
		}
		link, _ := sel.Find("td.title a").First().Attr("href")
		title, _ := sel.Find("td.title a").First().Attr("title")
		if link == "" {
			return
		}
		author := strings.TrimSpace(sel.Find("td").Eq(1).Find("a").Text())
		pub, err := parseDoubanListTime(sel.Find("td.time").First().Text())
		if err != nil {
			return // 时间解析失败跳过该条（列表时间必备）
		}
		items = append(items, ListItem{
			ExternalID:  topicIDFromURL(link),
			URL:         link,
			Title:       strings.TrimSpace(title),
			Author:      author,
			PublishedAt: pub,
		})
	})
	// 下一页游标：有条目 → 同组下一页；空页 → 下一组
	next := ""
	if len(items) > 0 {
		next = strconv.Itoa(gi) + ":" + strconv.Itoa(offset+25)
	} else if gi+1 < len(d.groupURLs) {
		next = strconv.Itoa(gi+1) + ":0"
	}
	return items, next, nil
}

// parseListCursor 解析 "组下标:偏移" 游标；非法/空 → 0:0
func parseListCursor(cursor string) (groupIdx, offset int) {
	if cursor == "" {
		return 0, 0
	}
	parts := strings.SplitN(cursor, ":", 2)
	groupIdx, _ = strconv.Atoi(parts[0])
	if len(parts) == 2 {
		offset, _ = strconv.Atoi(parts[1])
	}
	return groupIdx, offset
}

// get GET 请求带 cookie；风控响应转错误
func (d *Douban) get(ctx context.Context, rawURL string) (string, error) {
	cookie, _ := d.cookie.Get(ctx, "douban")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	// 浏览器 UA + cookie（豆瓣对无 UA 请求风控严格）
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(b)
	if riskDetected(body) {
		return "", fmt.Errorf("触发风控: %s", rawURL)
	}
	return body, nil
}

// topicIDFromURL 从详情链接提取 topic ID（/group/topic/111/ → 111）
func topicIDFromURL(link string) string {
	parts := strings.Split(strings.TrimSuffix(link, "/"), "/")
	return parts[len(parts)-1]
}

// parseDoubanListTime 解析列表时间（迁移参考仓库 timeparse.go）：
// "2006-01-02 15:04" / "01-02 15:04"（跨年减 1 年）/ "2006-01-02"
func parseDoubanListTime(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, fmt.Errorf("empty list time")
	}
	now := time.Now()
	layouts := []struct {
		layout string
		value  string
	}{
		{"2006-01-02 15:04", text},
		{"2006-01-02 15:04", fmt.Sprintf("%d-%s", now.Year(), text)},
		{"2006-01-02", text},
	}
	for _, item := range layouts {
		if t, err := time.ParseInLocation(item.layout, item.value, time.Local); err == nil {
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported list time: %q", raw)
}
