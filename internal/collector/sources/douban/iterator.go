package douban

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"rent-scout/internal/collector"
)

// doubanState 黑盒进度：组下标、页偏移、停止线
type doubanState struct {
	ActiveGroup int       `json:"g"`
	Offset      int       `json:"o"`
	StopLine    time.Time `json:"s"`
}

// DoubanIterator 豆瓣小组线性翻页；空页或整页旧数据就换组
type DoubanIterator struct {
	d       *Douban
	start   time.Time
	end     time.Time
	state   doubanState
	current []collector.ListItem
	err     error
}

// =============================================================================
// collector.Source：NewIterator
// =============================================================================

func (d *Douban) NewIterator(state string, start, end time.Time) collector.Iterator {
	var s doubanState
	s.StopLine = start
	if state != "" {
		_ = json.Unmarshal([]byte(state), &s)
	}
	return &DoubanIterator{
		d:     d,
		start: start,
		end:   end,
		state: s,
	}
}

func (it *DoubanIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}
	groups := it.d.groups()
	if len(groups) == 0 || it.state.ActiveGroup >= len(groups) {
		return false
	}

	targetURL := groups[it.state.ActiveGroup]
	u, err := url.Parse(targetURL)
	if err != nil {
		it.err = err
		return false
	}
	q := u.Query()
	q.Set("start", strconv.Itoa(it.state.Offset))
	u.RawQuery = q.Encode()

	body, err := it.d.get(ctx, u.String())
	if err != nil {
		it.err = err
		return false
	}

	items, err := ParseList(body)
	if err != nil {
		it.err = err
		return false
	}

	// 空页：本组翻完，切下一组
	if len(items) == 0 {
		it.state.ActiveGroup++
		it.state.Offset = 0
		return it.Next(ctx)
	}

	// 只留停止线～end 之间的帖
	var fresh []collector.ListItem
	for _, item := range items {
		if !item.PublishedAt.Before(it.state.StopLine) && !item.PublishedAt.After(it.end) {
			fresh = append(fresh, item)
		}
	}

	it.current = fresh
	it.state.Offset += 25

	// 整页都旧了，说明翻过头，换组
	if len(fresh) == 0 {
		it.state.ActiveGroup++
		it.state.Offset = 0
		return it.Next(ctx)
	}

	return true
}

func (it *DoubanIterator) Value() []collector.ListItem {
	return it.current
}

func (it *DoubanIterator) Checkpoint() string {
	b, _ := json.Marshal(it.state)
	return string(b)
}

func (it *DoubanIterator) Err() error {
	return it.err
}
