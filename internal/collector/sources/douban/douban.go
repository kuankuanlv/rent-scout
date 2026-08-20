package douban

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/log"
	"rent-scout/internal/models"
)
var _ collector.Source = (*Douban)(nil)

const listPageSize = 25 // 豆瓣小组讨论列表每页条数

// DoubanOptions douban 适配器参数
type DoubanOptions struct {
	Config    *config.HotConfig // 生产每轮读小组 URL；测试可空
	GroupURLs []string          // 仅测试钉死 URL；生产留空
	Cookie    cookie.Provider
	Client    *http.Client
}

// Douban 豆瓣小组适配器（规格 4.3）：List 列表页 → Detail 详情页
type Douban struct {
	rt          *config.HotConfig
	fixedGroups []string // 测试用
	cookie      cookie.Provider
	client      *http.Client
}

// NewDouban 创建适配器。小组 URL 不在这里钉死，List 时再读。
func NewDouban(opts DoubanOptions) (*Douban, error) {
	if opts.Cookie == nil {
		opts.Cookie = noopCookie{}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Douban{rt: opts.Config, fixedGroups: append([]string(nil), opts.GroupURLs...), cookie: opts.Cookie, client: opts.Client}, nil
}

func (d *Douban) groups() []string {
	if len(d.fixedGroups) > 0 {
		return d.fixedGroups
	}
	if d.rt != nil {
		if app := d.rt.Get(); app != nil {
			return parseHTTPURLs(app.Collector.Douban.Groups)
		}
	}
	return nil
}

func parseHTTPURLs(lines []string) []string {
	var out []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if i := indexInlineHash(s); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			out = append(out, s)
		}
	}
	return out
}

func indexInlineHash(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if (s[i] == ' ' || s[i] == '\t') && s[i+1] == '#' {
			return i
		}
	}
	return -1
}

func (d *Douban) Name() string { return models.SourceDouban.String() }

type noopCookie struct{}

func (noopCookie) Get(context.Context, string) (string, error) { return "", nil }

// Detail 抓取详情页并归一化为 RentPost（只对未存在的新帖调用）。
// 正文 = 首帖纯文本（去掉图片）；Raw = 去图后的详情 HTML
func (d *Douban) Detail(ctx context.Context, item collector.ListItem) (models.RentPost, error) {
	body, err := d.get(ctx, item.URL)
	if err != nil {
		return models.RentPost{}, err
	}
	content, raw, title, err := ParseDetail(body, item)
	if err != nil {
		return models.RentPost{}, err
	}
	return models.RentPost{
		Source:      d.Name(),
		ExternalID:  item.ExternalID,
		URL:         item.URL,
		Title:       title,
		Content:     content,
		Author:      item.Author,
		PublishedAt: item.PublishedAt,
		Status:      models.PostStatusCollected,
		Raw:         raw,
	}, nil
}

// List 抓取一页讨论列表。cursor 格式 "组下标:偏移"（如 "0:25"；"" = 从第一组第一页）。
// 当前组该页有条目 → 同组下一页 "gi:offset+25"；空页 → 推进到下一组 "gi+1:0"；
// 最后一组结束或下标越界 → ""（无更多页）。多小组按配置 groups 轮转
func (d *Douban) List(ctx context.Context, cursor string) ([]collector.ListItem, string, error) {
	groups := d.groups()
	if len(groups) == 0 {
		log.Info(d.Name(), "当前配置豆瓣小组 URL 为空，无需执行")
		return nil, "", nil
	}
	gi, offset := parseListCursor(cursor)
	if gi >= len(groups) {
		return nil, "", nil // 已遍历全部小组：结束
	}
	// 组 URL 拼接 start 参数（豆瓣分页 start=0, pageSize, 2*pageSize...）
	u, err := url.Parse(groups[gi])
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
	items, err := ParseList(body)
	if err != nil {
		return nil, "", err
	}
	// 下一页游标：有条目 → 同组下一页；空页 → 下一组
	next := ""
	if len(items) > 0 {
		next = strconv.Itoa(gi) + ":" + strconv.Itoa(offset+listPageSize)
	} else if gi+1 < len(groups) {
		next = strconv.Itoa(gi+1) + ":0"
	}
	return items, next, nil
}

// SkipGroup 本组不用再翻了（撞水位/时间窗），直接下一组开头；没有下一组返回空
func (d *Douban) SkipGroup(cursor string) string {
	gi, _ := parseListCursor(cursor)
	if gi+1 >= len(d.groups()) {
		return ""
	}
	return strconv.Itoa(gi+1) + ":0"
}

func (d *Douban) DescribeCursor(cursor string) string {
	gi, off := parseListCursor(cursor)
	if off < 0 {
		off = 0
	}
	return fmt.Sprintf("组%d第%d页", gi+1, off/listPageSize+1)
}

func (d *Douban) WatermarkKey(cursor string) string {
	gi, _ := parseListCursor(cursor)
	groups := d.groups()
	if gi < 0 || gi >= len(groups) {
		return ""
	}
	return "group:" + doubanGroupID(groups[gi])
}

func (d *Douban) TimeOrdered(cursor string) bool {
	return true
}

func doubanGroupID(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "group" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return topicIDFromURL(raw)
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
	ck, err := d.cookie.Get(ctx, models.SourceDouban.String())
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	// 浏览器 UA + cookie（豆瓣对无 UA 请求风控严格）
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	if ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		log.Error(d.Name(), "请求失败", "url", rawURL, "err", err)
		return "", fmt.Errorf("请求失败 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(b)
	if cookie.RiskDetected(body) {
		log.Error(d.Name(), "cookie 已失效", "url", rawURL, "http", resp.StatusCode, "snippet", cookie.RiskSnippet(body))
		return "", fmt.Errorf("%w: %s", cookie.ErrCookieInvalid, rawURL)
	}
	return body, nil
}

