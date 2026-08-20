package weibo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/config"
	"rent-scout/internal/log"
	"rent-scout/internal/models"
)

// 编译断言：Source 满足接口。
var _ collector.Source = (*Source)(nil)

func (s *Source) Fingerprint(cfg *config.AppConfig) string {
	if cfg == nil {
		return s.Name()
	}
	var keys []string
	for _, id := range config.WeiboContainerIDs(cfg.Collector.Weibo.SuperTopics) {
		keys = append(keys, "super:"+id)
	}
	for _, id := range config.WeiboUIDs(cfg.Collector.Weibo.Users) {
		keys = append(keys, "user:"+id)
	}
	return s.Name() + "|" + cfg.Collector.Weibo.RangeFrom + "|" + hashLines(keys)
}

func (s *Source) TimeWindow(cfg *config.AppConfig, now time.Time) (time.Time, time.Time, error) {
	start, end, err := config.ResolveTimeRange(cfg.Collector.Weibo.RangeFrom, "now", now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("微博拉取范围: %w", err)
	}
	return start, end, nil
}

func (s *Source) RequestGap(cfg *config.AppConfig) time.Duration {
	return time.Duration(cfg.Collector.Weibo.Interval) * time.Second
}

func hashLines(lines []string) string {
	var h [8]byte
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	copy(h[:], sum[:8])
	return hex.EncodeToString(h[:])
}

const weiboStatusTime = "Mon Jan 02 15:04:05 -0700 2006"

type Options struct {
	Config      *config.HotConfig
	Users       []string
	SuperTopics []string
	AjaxBase    string // 测试用 PC ajax 前缀；空则 https://weibo.com
	MobileBase  string // 测试用手机域前缀；空则 https://m.weibo.cn
	Cookie      cookie.Provider
	Client      *http.Client
	DetailBase  string // 测试用详情前缀；空则 https://m.weibo.cn/detail
}

type Source struct {
	rt         *config.HotConfig
	fixedUsers []string
	fixedSuper []string
	ajaxBase   string
	mobileBase string
	cookie     cookie.Provider
	client     *http.Client
	detailBase string
	mu         sync.Mutex
	superSince map[string]string // 超话 id → 下一页 since_id JSON
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
		fixedUsers: append([]string(nil), opts.Users...),
		fixedSuper: append([]string(nil), opts.SuperTopics...),
		ajaxBase:   strings.TrimRight(opts.AjaxBase, "/"),
		mobileBase: strings.TrimRight(opts.MobileBase, "/"),
		cookie:     opts.Cookie,
		client:     opts.Client,
		detailBase: strings.TrimRight(opts.DetailBase, "/"),
		superSince: map[string]string{},
	}
}

func (s *Source) Name() string { return models.SourceWeibo.String() }

func (s *Source) users() []string {
	if len(s.fixedUsers) > 0 {
		return parseWeiboUIDs(s.fixedUsers)
	}
	if s.rt != nil {
		if app := s.rt.Get(); app != nil {
			return parseWeiboUIDs(app.Collector.Weibo.Users)
		}
	}
	return nil
}

func (s *Source) supers() []string {
	if len(s.fixedSuper) > 0 {
		return parseWeiboContainerIDs(s.fixedSuper)
	}
	if s.rt != nil {
		if app := s.rt.Get(); app != nil {
			return parseWeiboContainerIDs(app.Collector.Weibo.SuperTopics)
		}
	}
	return nil
}

func parseWeiboUIDLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") || (strings.HasPrefix(s, "#") && len(s) > 1 && (s[1] == ' ' || s[1] == '\t')) {
		return "", false
	}
	if i := indexInlineHash(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Path
	}
	s = strings.Trim(s, "/")
	if i := strings.Index(s, "/u/"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "u/")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return s, s != ""
}

func parseWeiboUIDs(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		id, ok := parseWeiboUIDLine(line)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func parseWeiboContainerLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "//") || (strings.HasPrefix(s, "#") && len(s) > 1 && (s[1] == ' ' || s[1] == '\t')) {
		return "", false
	}
	if i := indexInlineHash(s); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Path
	}
	s = strings.Trim(s, "/")
	if i := strings.Index(s, "/p/"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimPrefix(s, "p/")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "_-_"); i > 0 {
		s = s[:i]
	}
	if len(s) < 10 {
		return "", false
	}
	return s, true
}

func parseWeiboContainerIDs(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		id, ok := parseWeiboContainerLine(line)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
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

type crawlTarget struct {
	kind    string
	id      string
	label   string
	ordered bool
	wmKey   string
}

func (s *Source) targets() []crawlTarget {
	var out []crawlTarget
	for _, id := range s.supers() {
		out = append(out, crawlTarget{kind: "super", id: id, label: id, ordered: true, wmKey: "super:" + id})
	}
	for _, id := range s.users() {
		out = append(out, crawlTarget{kind: "user", id: id, label: id, ordered: true, wmKey: "user:" + id})
	}
	return out
}

type noopCookie struct{}

func (noopCookie) Get(context.Context, string) (string, error) { return "", nil }

func (s *Source) List(ctx context.Context, cursor string) ([]collector.ListItem, string, error) {
	start, end := s.defaultWindow()
	return s.ListInWindow(ctx, cursor, start, end)
}

func (s *Source) ListInWindow(ctx context.Context, cursor string, start, end time.Time) ([]collector.ListItem, string, error) {
	ts := s.targets()
	if len(ts) == 0 {
		log.Info(s.Name(), "当前配置微博超话、博主均为空，无需执行")
		return nil, "", nil
	}
	gi, offset := parseListCursor(cursor)
	if gi >= len(ts) {
		return nil, "", nil
	}
	t := ts[gi]
	var items []collector.ListItem
	var err error
	switch t.kind {
	case "super":
		items, err = s.listSuper(ctx, t, offset, start, end)
	default:
		items, err = s.listUser(ctx, t, offset, start, end)
	}
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > 0 {
		next = strconv.Itoa(gi) + ":" + nextPageOffset(offset)
	} else if gi+1 < len(ts) {
		next = strconv.Itoa(gi+1) + ":0"
	}
	return items, next, nil
}

func nextPageOffset(offset string) string {
	return strconv.Itoa(pageFromOffset(offset) + 1)
}

func pageFromOffset(offset string) int {
	page := 1
	if offset != "" && offset != "0" {
		page, _ = strconv.Atoi(offset)
		if page < 1 {
			page = 1
		}
	}
	return page
}

func (s *Source) SkipGroup(cursor string) string {
	gi, _ := parseListCursor(cursor)
	if gi+1 >= len(s.targets()) {
		return ""
	}
	return strconv.Itoa(gi+1) + ":0"
}

func (s *Source) DescribeCursor(cursor string) string {
	gi, off := parseListCursor(cursor)
	ts := s.targets()
	name := fmt.Sprintf("目标%d", gi+1)
	if gi >= 0 && gi < len(ts) {
		t := ts[gi]
		switch t.kind {
		case "super":
			name = "超话「" + t.label + "」"
		default:
			name = "博主「" + t.label + "」"
		}
	}
	page := pageFromOffset(off)
	return fmt.Sprintf("%s第%d页", name, page)
}

func (s *Source) WatermarkKey(cursor string) string {
	gi, _ := parseListCursor(cursor)
	ts := s.targets()
	if gi < 0 || gi >= len(ts) {
		return ""
	}
	return ts[gi].wmKey
}

func (s *Source) TimeOrdered(cursor string) bool {
	gi, _ := parseListCursor(cursor)
	ts := s.targets()
	if gi < 0 || gi >= len(ts) {
		return true
	}
	return ts[gi].ordered
}

func (s *Source) sinceFor(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.superSince == nil {
		return ""
	}
	return s.superSince[id]
}

func (s *Source) setSince(id, since string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.superSince == nil {
		s.superSince = map[string]string{}
	}
	if strings.TrimSpace(since) == "" {
		delete(s.superSince, id)
		return
	}
	s.superSince[id] = since
}

func (s *Source) pcBase() string {
	if s.ajaxBase != "" {
		return s.ajaxBase
	}
	return "https://weibo.com"
}

func (s *Source) mBase() string {
	if s.mobileBase != "" {
		return s.mobileBase
	}
	return "https://m.weibo.cn"
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

// Detail 短帖用列表正文；长微博再补全文。正文抠不到联系方式和价格时，只捞博主自己的评论。
func (s *Source) Detail(ctx context.Context, item collector.ListItem) (models.RentPost, error) {
	content := stripUnfoldChrome(item.Content)
	if item.Kind == "user" && item.NeedDetail && strings.TrimSpace(item.MblogID) != "" {
		if full := s.fetchLongText(ctx, item.MblogID); full != "" {
			content = full
		}
	} else if item.NeedDetail && strings.TrimSpace(item.ExternalID) != "" && item.Kind != "user" {
		base := s.detailBase
		if base == "" {
			base = s.mBase() + "/detail"
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
	if item.AuthorID != "" && !models.HasContact(models.ExtractContact("", content)) && models.ExtractRentPrice("", content) == models.PriceUnknown {
		if extra := s.fetchOwnerComments(ctx, item); extra != "" {
			content = strings.TrimSpace(content + "\n" + extra)
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

func parseListCursor(cursor string) (groupIdx int, offset string) {
	if cursor == "" {
		return 0, "0"
	}
	parts := strings.SplitN(cursor, ":", 2)
	groupIdx, _ = strconv.Atoi(parts[0])
	if len(parts) == 2 {
		offset = parts[1]
	}
	if offset == "" {
		offset = "0"
	}
	return groupIdx, offset
}

func (s *Source) cookieSourceFor(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && strings.Contains(u.Host, "weibo.cn") {
		return "weibo.cn"
	}
	return models.SourceWeibo.String()
}

func (s *Source) get(ctx context.Context, rawURL string) (string, error) {
	return s.doGet(ctx, rawURL, true)
}

func (s *Source) getSoft(ctx context.Context, rawURL string) (string, error) {
	return s.doGet(ctx, rawURL, false)
}

func (s *Source) doGet(ctx context.Context, rawURL string, strict bool) (string, error) {
	log.Info(s.Name(), "请求 "+rawURL)
	ck, err := s.cookie.Get(ctx, s.cookieSourceFor(rawURL))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", "https://weibo.com/")
	if u, err := url.Parse(rawURL); err == nil {
		if strings.Contains(u.Host, "m.weibo.cn") || strings.Contains(u.Host, "weibo.cn") {
			req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1")
			req.Header.Set("Referer", "https://m.weibo.cn/")
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req.Header.Set("Accept", "application/json, text/plain, */*")
		} else if strings.Contains(u.Path, "/ajax") {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			req.Header.Set("Accept", "application/json, text/plain, */*")
			req.Header.Set("Referer", "https://weibo.com/")
		}
	}
	if ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if strict {
			log.Error(s.Name(), "请求失败", "url", rawURL, "err", err)
		}
		return "", fmt.Errorf("请求失败 %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	body := string(b)
	if err := weiboResponseErr(resp.StatusCode, body); err != nil {
		if !strict {
			return body, err
		}
		log.Error(s.Name(), "微博请求异常", "url", rawURL, "http", resp.StatusCode, "err", err)
		return "", err
	}
	return body, nil
}

func weiboResponseErr(status int, body string) error {
	if status == 403 || status == 418 || status == 432 {
		return fmt.Errorf("%w: http %d", cookie.ErrCookieInvalid, status)
	}
	trim := strings.TrimSpace(body)
	if strings.HasPrefix(trim, "{") {
		var head struct {
			OK  json.RawMessage `json:"ok"`
			URL string          `json:"url"`
		}
		if err := json.Unmarshal([]byte(trim), &head); err == nil && len(head.OK) > 0 {
			ok := strings.TrimSpace(string(head.OK))
			if ok != "1" && ok != `"1"` && ok != "true" {
				if ok == "-100" || strings.Contains(head.URL, "passport.weibo.com") || strings.Contains(head.URL, "sso/signin") {
					return fmt.Errorf("%w: ok=%s", cookie.ErrCookieInvalid, ok)
				}
				// ok=0 常见是没内容/没评论，当空结果，别打挂整轮
				return nil
			}
		}
	}
	if weiboRiskDetected(body) {
		if strings.Contains(body, "404") && strings.Contains(strings.ToLower(body), "error") {
			return fmt.Errorf("微博接口返回错误页")
		}
		return fmt.Errorf("%w: 登录墙", cookie.ErrCookieInvalid)
	}
	return nil
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

