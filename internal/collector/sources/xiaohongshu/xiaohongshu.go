package xiaohongshu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

var _ collector.Source = (*Xiaohongshu)(nil)

type Options struct {
	Config    *config.HotConfig
	Signer    Signer
	Cookie    cookie.Provider
	Client    *http.Client
	SearchURL string // test override, default https://www.xiaohongshu.com
}

type Xiaohongshu struct {
	rt        *config.HotConfig
	signer    Signer
	cookie    cookie.Provider
	client    *http.Client
	searchURL string
}

func New(opts Options) *Xiaohongshu {
	if opts.Cookie == nil {
		opts.Cookie = noopCookie{}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Xiaohongshu{
		rt:        opts.Config,
		signer:    opts.Signer,
		cookie:    opts.Cookie,
		client:    opts.Client,
		searchURL: strings.TrimRight(opts.SearchURL, "/"),
	}
}

func (s *Xiaohongshu) Name() string { return models.SourceXiaohongshu.String() }

func (s *Xiaohongshu) Fingerprint(cfg *config.AppConfig) string {
	if cfg == nil {
		return s.Name()
	}
	var keys []string
	for _, k := range cfg.Collector.Xiaohongshu.Searches {
		if t := strings.TrimSpace(k); t != "" {
			keys = append(keys, "search:"+t)
		}
	}
	for _, k := range cfg.Collector.Xiaohongshu.Topics {
		if t := strings.TrimSpace(k); t != "" {
			keys = append(keys, "topic:"+t)
		}
	}
	for _, k := range cfg.Collector.Xiaohongshu.Users {
		if t := strings.TrimSpace(k); t != "" {
			keys = append(keys, "user:"+t)
		}
	}
	return s.Name() + "|" + cfg.Collector.Xiaohongshu.RangeFrom + "|" + hashLines(keys)
}

func (s *Xiaohongshu) Detail(_ context.Context, item collector.ListItem) (models.RentPost, error) {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = item.Content
		if len(title) > 40 {
			title = title[:40]
		}
	}
	return models.RentPost{
		Source:      s.Name(),
		ExternalID:  item.ExternalID,
		URL:         item.URL,
		Title:       title,
		Content:     item.Content,
		Author:      item.Author,
		PublishedAt: item.PublishedAt,
		Status:      models.PostStatusCollected,
		Raw:         item.Content,
	}, nil
}

func (s *Xiaohongshu) targets() []crawlTarget {
	var out []crawlTarget
	if s.rt != nil {
		if app := s.rt.Get(); app != nil {
			for _, k := range app.Collector.Xiaohongshu.Searches {
				if t := strings.TrimSpace(k); t != "" {
					out = append(out, crawlTarget{kind: "search", id: t, wmKey: "search:" + t})
				}
			}
			for _, k := range app.Collector.Xiaohongshu.Topics {
				if t := strings.TrimSpace(k); t != "" {
					out = append(out, crawlTarget{kind: "topic", id: t, wmKey: "topic:" + t})
				}
			}
			for _, k := range app.Collector.Xiaohongshu.Users {
				if t := strings.TrimSpace(k); t != "" {
					out = append(out, crawlTarget{kind: "user", id: t, wmKey: "user:" + t})
				}
			}
		}
	}
	return out
}

type crawlTarget struct {
	kind  string
	id    string
	wmKey string
}

func (s *Xiaohongshu) searchBase() string {
	if s.searchURL != "" {
		return s.searchURL
	}
	return "https://www.xiaohongshu.com"
}

func (s *Xiaohongshu) searchNotes(ctx context.Context, t crawlTarget, page int) ([]collector.ListItem, error) {
	// 构造搜索请求体，签名后发往 searchBase
	bodyMap := map[string]interface{}{
		"keyword":   t.id,
		"page":      page,
		"page_size": 20,
		"sort":      "time_descending",
	}
	bodyBytes, _ := json.Marshal(bodyMap)
	path := "/api/sns/web/v1/search/notes"
	// 原 signer.Sign(ctx, SignRequest...) 改为 direct SignHeaders 调用
	// 取出必要 cookie 和 body
	ckMap := map[string]string{}
	// 这里简化，实际应从 cookie.Provider 获取或传递
	if ck, err := s.cookie.Get(ctx, models.SourceXiaohongshu.String()); err == nil {
		// 简单拆分cookie
		for _, part := range strings.Split(ck, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 {
				ckMap[kv[0]] = kv[1]
			}
		}
	}
	
	signed, err := SignHeaders(path, bodyBytes, ckMap)
	if err != nil {
		return nil, err
	}
	url := s.searchBase() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	for k, v := range signed {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	ck, _ := s.cookie.Get(ctx, models.SourceXiaohongshu.String())
	if strings.TrimSpace(ck) != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败 %s: %w", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 461 || resp.StatusCode == 406 || resp.StatusCode == 401 {
		_ = ClassifyError(resp)
		pkglog.SourceWarn(s.Name(), "风控拦截", "code", resp.StatusCode, "target", t.id)
		return nil, fmt.Errorf("%w: http %d", cookie.ErrCookieInvalid, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return ParseSearchNotes(b)
}

func hashLines(lines []string) string {
	var h [8]byte
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	copy(h[:], sum[:8])
	return hex.EncodeToString(h[:])
}

type noopCookie struct{}

func (noopCookie) Get(context.Context, string) (string, error) { return "", nil }
