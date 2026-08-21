package xiaohongshu

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"rent-scout/internal/collector"
)

type xiaohongshuState struct {
	Idx    int               `json:"idx"`
	Offset string            `json:"offset"`
	WMs    map[string]string `json:"wms"`
}

type XiaohongshuIterator struct {
	s       *Xiaohongshu
	start   time.Time
	end     time.Time
	state   xiaohongshuState
	current []collector.ListItem
	err     error
}

func (s *Xiaohongshu) NewIterator(state string, start, end time.Time) collector.Iterator {
	st := xiaohongshuState{WMs: make(map[string]string)}
	if state != "" {
		_ = json.Unmarshal([]byte(state), &st)
		if st.WMs == nil {
			st.WMs = make(map[string]string)
		}
	}
	st.Idx = 0
	st.Offset = ""
	return &XiaohongshuIterator{s: s, start: start, end: end, state: st}
}

func (it *XiaohongshuIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}
	targets := it.s.targets()
	if len(targets) == 0 || it.state.Idx >= len(targets) {
		return false
	}
	for it.state.Idx < len(targets) {
		t := targets[it.state.Idx]
		stopLineStr := it.state.WMs[t.wmKey]
		stopLine := it.start
		if stopLineStr != "" {
			if ts, err := time.Parse(time.RFC3339Nano, stopLineStr); err == nil && ts.After(stopLine) {
				stopLine = ts
			}
		}
		page := 1
		if it.state.Offset != "" {
			if p, err := strconv.Atoi(it.state.Offset); err == nil && p >= 1 && p <= 5 {
				page = p
			}
		}
		if page > 5 {
			it.state.Idx++
			it.state.Offset = ""
			continue
		}
		items, err := it.s.searchNotes(ctx, t, page)
		if err != nil {
			it.err = err
			return false
		}
		var fresh []collector.ListItem
		for _, item := range items {
			if !item.PublishedAt.After(stopLine) {
				break
			}
			if item.PublishedAt.After(it.end) {
				continue
			}
			fresh = append(fresh, item)
		}
		if it.state.Offset == "" && len(items) > 0 {
			newest := items[0].PublishedAt
			if newest.After(stopLine) {
				it.state.WMs[t.wmKey] = newest.Format(time.RFC3339Nano)
			}
		}
		if len(fresh) > 0 {
			it.current = fresh
			nextPage := page + 1
			if nextPage > 5 {
				it.state.Idx++
				it.state.Offset = ""
			} else {
				it.state.Offset = strconv.Itoa(nextPage)
			}
			return true
		}
		it.state.Idx++
		it.state.Offset = ""
	}
	return false
}

func (it *XiaohongshuIterator) Value() []collector.ListItem { return it.current }
func (it *XiaohongshuIterator) Checkpoint() string {
	b, _ := json.Marshal(it.state)
	return string(b)
}
func (it *XiaohongshuIterator) Err() error { return it.err }
