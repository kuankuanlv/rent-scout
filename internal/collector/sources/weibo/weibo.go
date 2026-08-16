package weibo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

var _ collector.Source = (*Source)(nil)
var _ collector.GroupSkipper = (*Source)(nil)
var _ collector.TimeWindowLister = (*Source)(nil)

const listPageSize = 25 // 游标步进与豆瓣一致，映射到微博 page=offset/25+1

	type Options struct {
		Config     *config.HotConfig
		Tags       []string // 仅测试钉死；生产留空，List 时读配置
		Base       string   // 测试用搜索前缀；空则 https://s.weibo.com/weibo
		Cookie     cookie.Provider
		Client     *http.Client
		DetailBase string // 测试用详情前缀；空则 https://m.weibo.cn/detail
	}

	type Source struct {
		rt         *config.HotConfig
		fixedTags  []string
		base       string
		cookie     cookie.Provider
		client     *http.Client
		detailBase string
	}

	func New(opts Options) *Source {
		if opts.Cookie == nil {
			opts.Cookie = noopCookie{}
		}
		if opts.Client == nil {
			opts.Client = &http.Client{Timeout: 30 * time.Second}
		}
		return &Source{
			rt:         opts.Config,
			fixedTags:  append([]string(nil), opts.Tags...),
			base:       strings.TrimRight(opts.Base, "/"),
			cookie:     opts.Cookie,
			client:     opts.Client,
			detailBase: strings.TrimRight(opts.DetailBase, "/"),
		}
	}

	func (s *Source) Name() string { return models.SourceWeibo.String() }

	func (s *Source) tags() []string {
		if len(s.fixedTags) > 0 {
			return config.WeiboTags(s.fixedTags)
		}
		if s.rt != nil {
			if app := s.rt.Get(); app != nil {
				return config.WeiboTags(app.Collector.Weibo.Tags)
			}
		}
		return nil
	}

	type noopCookie struct{}

	func (noopCookie) Get(context.Context, string) (string, error) { return "", nil }

	// List 抓一页高级搜索。cursor 同豆瓣 "组下标:偏移"；偏移按 25 步进，请求时写成 page。
	func (s *Source) List(ctx context.Context, cursor string) ([]collector.ListItem, string, error) {
		start, end := s.defaultWindow()
		return s.ListInWindow(ctx, cursor, start, end)
	}

	func (s *Source) ListInWindow(ctx context.Context, cursor string, start, end time.Time) ([]collector.ListItem, string, error) {
		tags := s.tags()
		if len(tags) == 0 {
			pkglog.Component(pkglog.SourceCollector(s.Name())).Info("当前配置微博话题为空，无需执行")
			return nil, "", nil
		}
		gi, offset := parseListCursor(cursor)
		if gi >= len(tags) {
			return nil, "", nil
		}
			pageURL, err := advancedSearchURL(s.searchBase(), tags[gi], start, end, offset/listPageSize+1)
			if err != nil {
				return nil, "", err
			}
			pkglog.Component(pkglog.SourceCollector(s.Name())).Info("请求列表 "+pageURL)
			body, err := s.get(ctx, pageURL)
			if err != nil {
				return nil, "", err
			}
			items, err := parseSearchList(body)
			if err != nil {
				return nil, "", err
			}
			pkglog.Component(pkglog.SourceCollector(s.Name())).Info(fmt.Sprintf("列表解析 %d 条 %s", len(items), pageURL))
		next := ""
		if len(items) > 0 {
			next = strconv.Itoa(gi) + ":" + strconv.Itoa(offset+listPageSize)
		} else if gi+1 < len(tags) {
			next = strconv.Itoa(gi+1) + ":0"
		}
		return items, next, nil
	}

	func (s *Source) SkipGroup(cursor string) string {
		gi, _ := parseListCursor(cursor)
		if gi+1 >= len(s.tags()) {
			return ""
		}
		return strconv.Itoa(gi+1) + ":0"
	}

	func (s *Source) DescribeCursor(cursor string) string {
		gi, off := parseListCursor(cursor)
		page := off / listPageSize
		if off < 0 {
			off = 0
			page = 0
		}
		page++
		name := fmt.Sprintf("搜索%d", gi+1)
		tags := s.tags()
		if gi >= 0 && gi < len(tags) {
			name += "「" + tags[gi] + "」"
		}
		return fmt.Sprintf("%s第%d页", name, page)
	}

	func (s *Source) searchBase() string {
		if s.base != "" {
			return s.base
		}
		return "https://s.weibo.com/weibo"
	}

	func (s *Source) defaultWindow() (time.Time, time.Time) {
		now := time.Now()
		if s.rt != nil {
			if app := s.rt.Get(); app != nil {
				start, end, err := config.ResolveTimeRange(app.Collector.Weibo.RangeFrom, "now", now)
				if err == nil {
					return start, end
				}
			}
		}
		return now.Add(-10 * 24 * time.Hour), now
	}

	func advancedSearchURL(base, tag string, start, end time.Time, page int) (string, error) {
		if strings.TrimSpace(base) == "" {
			base = "https://s.weibo.com/weibo"
		}
		u, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("微博搜索地址非法: %w", err)
		}
		if u.Path == "" || u.Path == "/" {
			u.Path = "/weibo"
		}
		q := u.Query()
		q.Set("q", tag)
		q.Set("typeall", "1")
		q.Set("suball", "1")
		q.Set("timescope", weiboTimescope(start, end))
		q.Set("Refer", "g")
		if page < 1 {
			page = 1
		}
		q.Set("page", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		return u.String(), nil
	}

	func weiboTimescope(start, end time.Time) string {
		if start.IsZero() {
			start = time.Now().Add(-10 * 24 * time.Hour)
		}
		if end.IsZero() {
			end = time.Now()
		}
		if end.Before(start) {
			end = start
		}
		return "custom:" + weiboHour(start) + ":" + weiboHour(end)
	}

	func weiboHour(t time.Time) string {
		t = t.In(time.Local)
		return fmt.Sprintf("%s-%d", t.Format("2006-01-02"), t.Hour())
	}

	// Detail 短帖用列表正文；长微博列表只有摘要时再打 m.weibo.cn 详情拿全文。正文去图。
	func (s *Source) Detail(ctx context.Context, item collector.ListItem) (models.RentPost, error) {
		content := stripUnfoldChrome(item.Content)
		if item.NeedDetail && strings.TrimSpace(item.ExternalID) != "" {
			base := s.detailBase
			if base == "" {
				base = "https://m.weibo.cn/detail"
			}
			body, err := s.get(ctx, base+"/"+item.ExternalID)
			if err != nil {
				if content == "" {
					return models.RentPost{}, err
				}
			} else if full := parseMobileStatus(body); full != "" {
				content = full
			}
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = clipTitle(content)
		}
		return models.RentPost{
			Source:      s.Name(),
			ExternalID:  item.ExternalID,
			URL:         item.URL,
			Title:       title,
			Content:     content,
			Author:      item.Author,
			PublishedAt: item.PublishedAt,
			Status:      models.PostStatusCollected,
			Raw:         content,
		}, nil
	}

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

func (s *Source) get(ctx context.Context, rawURL string) (string, error) {
	pkglog.Component(pkglog.SourceCollector(s.Name())).Info("请求 " + rawURL)
	ck, err := s.cookie.Get(ctx, models.SourceWeibo.String())
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Referer", "https://s.weibo.com/")
		if u, err := url.Parse(rawURL); err == nil && strings.Contains(u.Host, "m.weibo.cn") {
			req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
			req.Header.Set("Referer", "https://m.weibo.cn/")
		}
	if ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		pkglog.Component(pkglog.SourceCollector(s.Name())).Error("请求失败", "url", rawURL, "err", err)
		return "", fmt.Errorf("请求失败 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	body := string(b)
	if weiboRiskDetected(body) {
		pkglog.Component(pkglog.SourceCollector(s.Name())).Error("cookie 已失效",
			"url", rawURL, "http", resp.StatusCode)
		return "", fmt.Errorf("%w: %s", cookie.ErrCookieInvalid, rawURL)
	}
	return body, nil
}

func weiboRiskDetected(body string) bool {
	for _, kw := range []string{
		"passport.weibo.com",
		"请先登录",
		"登录后查看",
		"Sina Visitor System",
		"login.php",
	} {
		if strings.Contains(body, kw) {
			return true
		}
	}
	return false
}

func parseSearchList(body string) ([]collector.ListItem, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析微博列表页: %w", err)
	}
	var items []collector.ListItem
	doc.Find("div.card-wrap").Each(func(_ int, sel *goquery.Selection) {
		mid, _ := sel.Attr("mid")
		mid = strings.TrimSpace(mid)
		if mid == "" || mid == "0" {
			return
		}
		txt, needDetail := cardContent(sel)
		if txt == "" {
			return
		}
		author := strings.TrimSpace(sel.Find("a.name").First().AttrOr("nick-name", ""))
		if author == "" {
			author = strings.TrimSpace(sel.Find("a.name").First().Text())
		}
		from := sel.Find("p.from a").First()
		href, _ := from.Attr("href")
		link := absWeiboURL(href)
		if link == "" {
			link = "https://weibo.com/detail/" + mid
		}
		pub, err := parseWeiboTime(from.AttrOr("title", ""), strings.TrimSpace(from.Text()))
		if err != nil {
			return
		}
		items = append(items, collector.ListItem{
			ExternalID:  mid,
			URL:         link,
			Title:       clipTitle(txt),
			Author:      author,
			PublishedAt: pub,
			Content:     txt,
			NeedDetail:  needDetail,
		})
	})
	return items, nil
}

func cardContent(sel *goquery.Selection) (string, bool) {
	full := stripUnfoldChrome(sel.Find(`p[node-type="feed_list_content_full"]`).First().Text())
	if full != "" {
		return full, false
	}
	txt := stripUnfoldChrome(sel.Find(`p[node-type="feed_list_content"]`).First().Text())
	if txt == "" {
		txt = stripUnfoldChrome(sel.Find("p.txt").First().Text())
	}
	need := sel.Find(`[action-type="fl_unfold"]`).Length() > 0
	return txt, need
}

func stripUnfoldChrome(s string) string {
	s = strings.ReplaceAll(s, "展开全文", "")
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "展开")
	s = strings.TrimSuffix(s, "收起")
	return collector.PlainText(s)
}

var renderDataRe = regexp.MustCompile(`(?s)\$render_data\s*=\s*(\[.*?])\s*(?:\|\||;)`)

func parseMobileStatus(body string) string {
	if m := renderDataRe.FindStringSubmatch(body); len(m) == 2 {
		var payload []struct {
			Status struct {
				Text string `json:"text"`
			} `json:"status"`
		}
		if err := json.Unmarshal([]byte(m[1]), &payload); err == nil {
			for _, p := range payload {
				if t := htmlToPlain(p.Status.Text); t != "" {
					return t
				}
			}
		}
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return ""
	}
	doc.Find("img").Remove()
	for _, sel := range []string{".weibo-text", ".weibo-og", "div.weibo-text"} {
		if t := collector.PlainText(doc.Find(sel).First().Text()); t != "" {
			return t
		}
	}
	return ""
}

func htmlToPlain(raw string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div>" + raw + "</div>"))
	if err != nil {
		return stripUnfoldChrome(raw)
	}
	doc.Find("img").Remove()
	return stripUnfoldChrome(doc.Text())
}

func absWeiboURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "/") {
		return "https://weibo.com" + href
	}
	return href
}

func clipTitle(s string) string {
	s = collapseSpace(s)
	if utf8.RuneCountInString(s) <= 40 {
		return s
	}
	return string([]rune(s)[:40]) + "…"
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// parseWeiboTime 优先 title="2006-01-02 15:04"；否则今天/昨天/N分钟前
func parseWeiboTime(title, text string) (time.Time, error) {
	now := time.Now()
	title = strings.TrimSpace(title)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, title, time.Local); err == nil {
			return t, nil
		}
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "今天") {
		rest := strings.TrimSpace(strings.TrimPrefix(text, "今天"))
		if t, err := time.ParseInLocation("15:04", rest, time.Local); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local), nil
		}
	}
	if strings.HasPrefix(text, "昨天") {
		rest := strings.TrimSpace(strings.TrimPrefix(text, "昨天"))
		if t, err := time.ParseInLocation("15:04", rest, time.Local); err == nil {
			d := now.AddDate(0, 0, -1)
			return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, time.Local), nil
		}
	}
	if strings.HasSuffix(text, "分钟前") {
		n, _ := strconv.Atoi(strings.TrimSuffix(text, "分钟前"))
		return now.Add(-time.Duration(n) * time.Minute), nil
	}
	if strings.HasSuffix(text, "秒前") {
		return now, nil
	}
	if t, err := time.ParseInLocation("01月02日 15:04", text, time.Local); err == nil {
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		if t.After(now.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported weibo time: %q %q", title, text)
}
