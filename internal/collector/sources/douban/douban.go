package douban

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/log"
	"rent-scout/internal/models"
)
var _ collector.Source = (*Douban)(nil)

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

