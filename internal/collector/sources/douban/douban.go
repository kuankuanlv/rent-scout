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
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

var _ collector.Source = (*Douban)(nil)

// DoubanOptions 构造参数
type DoubanOptions struct {
	Config    *config.HotConfig // 热配置；生产读小组 URL；测试可空
	GroupURLs []string          // 仅测试钉死小组 URL；生产留空走 Config
	Cookie    cookie.Provider   // cookie 提供器；空则 noop
	Client    *http.Client      // HTTP 客户端；空则 30s 超时默认
}

// Douban 豆瓣小组采集源
type Douban struct {
	rt          *config.HotConfig // 热配置，每轮读小组清单
	fixedGroups []string          // 测试钉死的小组 URL；非空时优先生效
	cookie      cookie.Provider   // 取豆瓣 cookie
	client      *http.Client      // 发列表/详情请求
}

// NewDouban 创建适配器；小组 URL 不在这里钉死，运行时再读。
func NewDouban(opts DoubanOptions) (*Douban, error) {
	if opts.Cookie == nil {
		opts.Cookie = noopCookie{}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Douban{
		rt:          opts.Config,
		fixedGroups: append([]string(nil), opts.GroupURLs...),
		cookie:      opts.Cookie,
		client:      opts.Client,
	}, nil
}

// =============================================================================
// collector.Source 实现（Name / NewIterator 在 iterator.go / Detail）
// =============================================================================

func (d *Douban) Name() string { return models.SourceDouban.String() }

// Detail 抓详情并归一化为 RentPost（只对未入库的新帖调用）。
// 正文 = 首帖纯文本（去图）；Raw = 去图后的详情 HTML。
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

// =============================================================================
// 私有：配置 / 目标
// =============================================================================

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

// =============================================================================
// 私有：HTTP
// =============================================================================

// get GET 带 cookie；风控响应转 ErrCookieInvalid
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
		pkglog.SourceError(d.Name(), "请求失败", "url", rawURL, "err", err)
		return "", fmt.Errorf("请求失败 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(b)
	if cookie.RiskDetected(body) {
		pkglog.SourceError(d.Name(), "cookie 已失效", "url", rawURL, "http", resp.StatusCode, "snippet", cookie.RiskSnippet(body))
		return "", fmt.Errorf("%w: %s", cookie.ErrCookieInvalid, rawURL)
	}
	return body, nil
}

// =============================================================================
// 包级工具
// =============================================================================

type noopCookie struct{}

func (noopCookie) Get(context.Context, string) (string, error) { return "", nil }

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
