package weibo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"rent-scout/internal/collector"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/models"
)

const WeiboStatusTime = "Mon Jan 02 15:04:05 -0700 2006"

type profileResp struct {
	OK   json.RawMessage `json:"ok"`
	Data struct {
		List []profileStatus `json:"list"`
	} `json:"data"`
}

type profileStatus struct {
	ID         json.RawMessage `json:"id"`
	IDStr      string          `json:"idstr"`
	Mid        string          `json:"mid"`
	MblogID    string          `json:"mblogid"`
	CreatedAt  string          `json:"created_at"`
	TextRaw    string          `json:"text_raw"`
	IsLongText bool            `json:"isLongText"`
	User       struct {
		ID         json.RawMessage `json:"id"`
		IDStr      string          `json:"idstr"`
		ScreenName string          `json:"screen_name"`
	} `json:"user"`
}

func ParseProfileList(body, uid string) ([]collector.ListItem, error) {
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
		pub, err := time.Parse(WeiboStatusTime, st.CreatedAt)
		if err != nil {
			pkglog.SourceWarn(models.SourceWeibo.String(), "博主帖时间解析失败", "mid", mid, "raw", st.CreatedAt)
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

func jsonNum(raw json.RawMessage) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "null" || s == "" {
		return ""
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
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
