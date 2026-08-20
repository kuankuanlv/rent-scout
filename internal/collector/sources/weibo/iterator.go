package weibo

import (
	"context"
	"encoding/json"
	"time"

	"rent-scout/internal/collector"
)

// weiboState 多目标混合进度：各目标停止线（水位）。
// 注意：ActiveIdx/Offset 是「本轮内」的遍历游标，不跨轮持久化——
// 若持久化，一旦卡在某个目标段（如博主 0 条），前面的目标（超话）会被永久跳过。
// 每轮从第一个目标、第一页开始，靠 Watermarks 水位撞线判断停止，保证所有目标都被检查。
type weiboState struct {
	ActiveIdx  int               `json:"idx"`    // 本轮内目标下标（不持久化，NewIterator 归零）
	Offset     string            `json:"offset"` // 本轮内翻页偏移（不持久化，NewIterator 归零）
	Watermarks map[string]string `json:"wms"`    // 目标 → 已采到的最新帖时间（持久化，撞线判断）
}

// WeiboIterator 超话+博主混合流；降序翻到停止线就换目标
type WeiboIterator struct {
	s       *Weibo
	start   time.Time
	end     time.Time
	state   weiboState
	current []collector.ListItem
	err     error
}

// =============================================================================
// collector.Source：NewIterator
// =============================================================================

func (s *Weibo) NewIterator(state string, start, end time.Time) collector.Iterator {
	st := weiboState{
		Watermarks: make(map[string]string),
	}
	if state != "" {
		_ = json.Unmarshal([]byte(state), &st)
	}
	// 每轮从第一个目标、第一页开始；只保留 Watermarks 水位。
	// 若沿用上次的 ActiveIdx/Offset，博主 0 条时游标卡住，超话等前面的目标会被永久跳过。
	st.ActiveIdx = 0
	st.Offset = ""
	return &WeiboIterator{
		s:     s,
		start: start,
		end:   end,
		state: st,
	}
}

func (it *WeiboIterator) Next(ctx context.Context) bool {
	if it.err != nil {
		return false
	}

	targets := it.s.targets()
	if len(targets) == 0 || it.state.ActiveIdx >= len(targets) {
		return false
	}

	for it.state.ActiveIdx < len(targets) {
		t := targets[it.state.ActiveIdx]

		// 停止线取「时间窗起点」和「本目标水位」里更晚的
		wmKey := t.wmKey
		stopLineStr := it.state.Watermarks[wmKey]
		stopLine := it.start
		if stopLineStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, stopLineStr); err == nil {
				if t.After(stopLine) {
					stopLine = t
				}
			}
		}

		var items []collector.ListItem
		var err error
		if t.kind == "super" {
			items, err = it.s.listSuper(ctx, t, it.state.Offset, it.start, it.end)
		} else {
			items, err = it.s.listUser(ctx, t, it.state.Offset, it.start, it.end)
		}

		if err != nil {
			it.err = err
			return false
		}

		var fresh []collector.ListItem
		for _, item := range items {
			// 降序：不晚于停止线就撞线，后面不用看了
			if !item.PublishedAt.After(stopLine) {
				break
			}
			if item.PublishedAt.After(it.end) {
				continue
			}
			fresh = append(fresh, item)
		}

		// 首页有新帖就抬高本目标水位
		if it.state.Offset == "" || it.state.Offset == "0" {
			if len(items) > 0 {
				newest := items[0].PublishedAt
				if newest.After(stopLine) {
					it.state.Watermarks[wmKey] = newest.Format(time.RFC3339Nano)
				}
			}
		}

		if len(fresh) > 0 {
			it.current = fresh
			it.state.Offset = nextPageOffset(it.state.Offset)
			return true
		}

		// 没新帖或撞线，换下一个目标
		it.state.ActiveIdx++
		it.state.Offset = ""
	}

	return false
}

func (it *WeiboIterator) Value() []collector.ListItem {
	return it.current
}

func (it *WeiboIterator) Checkpoint() string {
	b, _ := json.Marshal(it.state)
	return string(b)
}

func (it *WeiboIterator) Err() error {
	return it.err
}
