package xiaohongshu

import (
	"encoding/json"
	"fmt"
	"time"

	"rent-scout/internal/collector"
)

type searchResponse struct {
	Data struct {
		Items []struct {
			ID       string `json:"id"`
			NoteCard struct {
				Title string `json:"display_title"`
				Desc  string `json:"desc"`
				User  struct {
					Nickname string `json:"nickname"`
				} `json:"user"`
				Time int64 `json:"time"`
			} `json:"note_card"`
		} `json:"items"`
	} `json:"data"`
}

func ParseSearchNotes(data []byte) ([]collector.ListItem, error) {
	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decoding fail: %w", err)
	}

	var items []collector.ListItem
	for _, item := range resp.Data.Items {
		items = append(items, collector.ListItem{
			ExternalID:  item.ID,
			Title:       item.NoteCard.Title,
			Content:     item.NoteCard.Desc,
			Author:      item.NoteCard.User.Nickname,
			PublishedAt: time.Unix(item.NoteCard.Time/1000, 0),
		})
	}
	return items, nil
}
