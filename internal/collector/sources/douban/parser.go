package douban

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"rent-scout/internal/collector"
)

const ListPageSize = 25

func ParseDetail(body string, item collector.ListItem) (string, string, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return "", "", "", fmt.Errorf("解析详情页: %w", err)
	}
	doc.Find("img").Remove()
	box := doc.Find(".topic-content").First()
	content := collector.PlainText(box.Text())
	raw, _ := doc.Html()
	title := item.Title
	if title == "" {
		title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	return content, raw, title, nil
}

func ParseList(body string) ([]collector.ListItem, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析列表页: %w", err)
	}

	var items []collector.ListItem
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
			return 
		}
		items = append(items, collector.ListItem{
			ExternalID:  topicIDFromURL(link),
			URL:         link,
			Title:       strings.TrimSpace(title),
			Author:      author,
			PublishedAt: pub,
		})
	})
	return items, nil
}

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

func topicIDFromURL(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	return parts[len(parts)-1]
}
