package weibo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/cookie"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
)

func (s *Source) listUser(ctx context.Context, t crawlTarget, offset string, start, end time.Time) ([]collector.ListItem, error) {
	page := pageFromOffset(offset)
	u, err := url.Parse(s.pcBase() + "/ajax/statuses/searchProfile")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("uid", t.id)
	q.Set("page", strconv.Itoa(page))
	q.Set("hasori", "1")
	q.Set("starttime", strconv.FormatInt(start.Unix(), 10))
	q.Set("endtime", strconv.FormatInt(end.Unix(), 10))
	u.RawQuery = q.Encode()
	body, err := s.get(ctx, u.String())
	if err != nil {
		return nil, err
	}
	items, err := parseProfileList(body, t.id)
	if err != nil {
		return nil, err
	}
	pkglog.Component(pkglog.SourceCollector(s.Name())).Info(fmt.Sprintf("博主列表解析 %d 条 %s", len(items), u.String()))
	return items, nil
}

func parseProfileList(body, uid string) ([]collector.ListItem, error) {
	var resp profileResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("解析博主列表: %w", err)
	}
	var items []collector.ListItem
	for _, st := range resp.Data.List {
		mid := firstNonEmpty(st.Mid, st.IDStr, jsonNum(st.ID))
		if mid == "" {
			continue
		}
		pub, err := time.Parse(weiboStatusTime, st.CreatedAt)
		if err != nil {
			pkglog.Component(pkglog.SourceCollector(models.SourceWeibo.String())).Warn("博主帖时间解析失败", "mid", mid, "raw", st.CreatedAt)
			continue
		}
		txt := strings.TrimSpace(st.TextRaw)
		uidVal := firstNonEmpty(st.User.IDStr, jsonNum(st.User.ID), uid)
		link := "https://weibo.com/" + uidVal + "/" + firstNonEmpty(st.MblogID, mid)
		items = append(items, collector.ListItem{
			ExternalID:  mid,
			URL:         link,
			Title:       clipTitle(txt),
			Author:      st.User.ScreenName,
			AuthorID:    uidVal,
			MblogID:     st.MblogID,
			Kind:        "user",
			PublishedAt: pub,
			Content:     txt,
			NeedDetail:  st.IsLongText,
		})
	}
	return items, nil
}

func (s *Source) listSuper(ctx context.Context, t crawlTarget, offset string, start, end time.Time) ([]collector.ListItem, error) {
	ck, err := s.cookie.Get(ctx, models.SourceWeibo.String())
	if err != nil && !errors.Is(err, cookie.ErrCookieMissing) {
		return nil, err
	}
	if strings.TrimSpace(ck) == "" {
		pkglog.Component(pkglog.SourceCollector(s.Name())).Warn("weibo.com cookie 为空，跳过超话", "id", t.id)
		return nil, nil
	}
	page := pageFromOffset(offset)
	u, err := url.Parse(s.pcBase() + "/ajax_proxy/chaohua/page")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("flowId", superFeedID(t.id))
	if page >= 2 {
		since := s.sinceFor(t.id)
		if since == "" {
			return nil, nil
		}
		q.Set("since_id", since)
		q.Set("count", "15")
		q.Set("max_id", "0")
		feed := page - 1
		q.Set("page_common_ext", fmt.Sprintf("topicPrompt:1|page:feed=%d|hide_page:%d", feed, feed))
	} else {
		s.setSince(t.id, "")
	}
	u.RawQuery = q.Encode()
	body, err := s.get(ctx, u.String())
	if err != nil {
		return nil, err
	}
	items, since, err := parseChaohuaPage(body)
	if err != nil {
		return nil, err
	}
	s.setSince(t.id, since)
	items = filterWindow(items, start, end)
	pkglog.Component(pkglog.SourceCollector(s.Name())).Info(fmt.Sprintf("超话列表解析 %d 条 %s", len(items), u.String()))
	return items, nil
}

func superFeedID(id string) string {
	id = strings.TrimSpace(id)
	if strings.Contains(id, "_-_") {
		return id
	}
	return id + "_-_feed"
}

func parseChaohuaPage(body string) ([]collector.ListItem, string, error) {
	var root any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return nil, "", fmt.Errorf("解析超话列表: %w", err)
	}
	seen := map[string]bool{}
	var items []collector.ListItem
	walkFeedMblogs(root, seen, &items)
	return items, extractSinceID(root), nil
}

func extractSinceID(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	more, _ := m["moreInfo"].(map[string]any)
	if more == nil {
		return ""
	}
	params, _ := more["params"].(map[string]any)
	if params == nil {
		return ""
	}
	switch x := params["since_id"].(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
}

func walkFeedMblogs(v any, seen map[string]bool, items *[]collector.ListItem) {
	switch t := v.(type) {
	case map[string]any:
		if _, hasUser := t["user"]; hasUser {
			if _, hasTime := t["created_at"]; hasTime {
				if it, ok := listItemFromFeedMap(t); ok && !seen[it.ExternalID] {
					seen[it.ExternalID] = true
					*items = append(*items, it)
				}
			}
		}
		for k, c := range t {
			if k == "user" {
				continue
			}
			walkFeedMblogs(c, seen, items)
		}
	case []any:
		for _, c := range t {
			walkFeedMblogs(c, seen, items)
		}
	}
}

func listItemFromFeedMap(m map[string]any) (collector.ListItem, bool) {
	mid := firstNonEmpty(anyID(m["mid"]), anyID(m["id"]))
	if mid == "" || mid == "<nil>" {
		return collector.ListItem{}, false
	}
	created := fmt.Sprint(m["created_at"])
	pub, err := time.Parse(weiboStatusTime, created)
	if err != nil {
		pkglog.Component(pkglog.SourceCollector(models.SourceWeibo.String())).Warn("超话帖时间解析失败", "mid", mid, "raw", created)
		return collector.ListItem{}, false
	}
	txt := firstNonEmpty(strAny(m["text_raw"]), strAny(m["text"]))
	if strings.Contains(txt, "<") {
		txt = htmlToPlain(txt)
	} else {
		txt = strings.TrimSpace(txt)
	}
	user, _ := m["user"].(map[string]any)
	uid := ""
	name := ""
	if user != nil {
		uid = firstNonEmpty(anyID(user["idstr"]), anyID(user["id"]))
		name = strAny(user["screen_name"])
	}
	bid := firstNonEmpty(anyID(m["mblogid"]), anyID(m["bid"]))
	link := "https://m.weibo.cn/detail/" + mid
	if uid != "" && bid != "" && bid != "<nil>" {
		link = "https://weibo.com/" + uid + "/" + bid
	}
	long := false
	switch x := m["isLongText"].(type) {
	case bool:
		long = x
	case float64:
		long = x != 0
	}
	return collector.ListItem{
		ExternalID:  mid,
		URL:         link,
		Title:       clipTitle(txt),
		Author:      name,
		AuthorID:    uid,
		MblogID:     bid,
		Kind:        "super",
		PublishedAt: pub,
		Content:     txt,
		NeedDetail:  long,
	}, true
}

func filterWindow(items []collector.ListItem, start, end time.Time) []collector.ListItem {
	var out []collector.ListItem
	for _, it := range items {
		if !start.IsZero() && it.PublishedAt.Before(start) {
			continue
		}
		if !end.IsZero() && it.PublishedAt.After(end) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (s *Source) fetchLongText(ctx context.Context, mblogID string) string {
	u := s.pcBase() + "/ajax/statuses/longtext?id=" + url.QueryEscape(mblogID)
	body, err := s.getSoft(ctx, u)
	if err != nil {
		return ""
	}
	var resp struct {
		Data struct {
			LongTextContent string `json:"longTextContent"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return ""
	}
	return strings.TrimSpace(htmlToPlain(resp.Data.LongTextContent))
}

func (s *Source) fetchOwnerComments(ctx context.Context, item collector.ListItem) string {
	rawURL := s.pcBase() + "/ajax/statuses/buildComments?is_reload=1&id=" + url.QueryEscape(item.ExternalID) + "&is_show_bulletin=2&count=20"
	if item.Kind == "super" {
		rawURL = s.mBase() + "/comments/hotflow?id=" + url.QueryEscape(item.ExternalID) + "&mid=" + url.QueryEscape(item.ExternalID) + "&max_id_type=0"
	}
	body, err := s.getSoft(ctx, rawURL)
	if err != nil {
		return ""
	}
	return ownerCommentText(body, item.AuthorID)
}

func ownerCommentText(body, authorID string) string {
	var wrap map[string]any
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		return ""
	}
	var texts []string
	var walk func(any)
	walk = func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			if arr, ok := v.([]any); ok {
				for _, x := range arr {
					walk(x)
				}
			}
			return
		}
		if user, ok := m["user"].(map[string]any); ok {
			uid := fmt.Sprint(user["id"])
			if idstr, ok := user["idstr"].(string); ok && idstr != "" {
				uid = idstr
			}
			if uid == authorID {
				if t, ok := m["text"].(string); ok {
					if p := htmlToPlain(t); p != "" {
						texts = append(texts, p)
					}
				}
			}
		}
		for _, x := range m {
			walk(x)
		}
	}
	walk(wrap)
	return strings.Join(texts, "\n")
}

func anyID(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	default:
		s := strings.TrimSpace(fmt.Sprint(x))
		if s == "<nil>" {
			return ""
		}
		return s
	}
}

func strAny(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}
