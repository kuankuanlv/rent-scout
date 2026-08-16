package pkglog

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

const subBuf = 64

const (
	defaultMemoryLines = 1000
	minMemoryLines     = 100
	maxMemoryLines     = 10000
)

// Line 一条进 ring / SSE 的日志（给管理台滚动查看）
type Line struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Duty    string    `json:"duty"`
	Message string    `json:"msg"`
	Attrs   string    `json:"attrs,omitempty"`
}

// Hub 内存 ring + 订阅扇出；满了丢最老的，订阅阻塞就丢这条（不堵业务日志）
type Hub struct {
	mu   sync.Mutex
	cap  int
	seq  uint64
	buf  []Line
	subs map[chan Line]struct{}
}

func newHub(cap int) *Hub {
	if cap <= 0 {
		cap = defaultMemoryLines
	}
	return &Hub{cap: cap, buf: make([]Line, 0, cap), subs: map[chan Line]struct{}{}}
}

var defaultHub = newHub(defaultMemoryLines)

func clampHubCap(n int) int {
	if n <= 0 {
		return defaultMemoryLines
	}
	if n < minMemoryLines {
		return minMemoryLines
	}
	if n > maxMemoryLines {
		return maxMemoryLines
	}
	return n
}

func (h *Hub) setCap(n int) {
	n = clampHubCap(n)
	h.mu.Lock()
	defer h.mu.Unlock()
	if n == h.cap {
		return
	}
	h.cap = n
	if len(h.buf) > n {
		h.buf = append([]Line(nil), h.buf[len(h.buf)-n:]...)
	}
}

// SetHubCap 热更新 ring 容量；缩小丢掉最老的，订阅者不停
func SetHubCap(n int) { defaultHub.setCap(n) }

// HubCap 当前 ring 容量
func HubCap() int {
	defaultHub.mu.Lock()
	defer defaultHub.mu.Unlock()
	return defaultHub.cap
}

func (h *Hub) push(line Line) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	line.Seq = h.seq
	if len(h.buf) >= h.cap {
		copy(h.buf, h.buf[1:])
		h.buf[len(h.buf)-1] = line
	} else {
		h.buf = append(h.buf, line)
	}
	for ch := range h.subs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (h *Hub) recent(n int) []Line {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n <= 0 || n > len(h.buf) {
		n = len(h.buf)
	}
	out := make([]Line, n)
	copy(out, h.buf[len(h.buf)-n:])
	return out
}

func (h *Hub) subscribe() (<-chan Line, func()) {
	ch := make(chan Line, subBuf)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		close(ch)
	}
}

// RecentLogs 最近 n 条（给页面首屏 / JSON）
func RecentLogs(n int) []Line { return defaultHub.recent(n) }

// SubscribeLogs 订阅新日志；调用 cancel 退订
func SubscribeLogs() (<-chan Line, func()) { return defaultHub.subscribe() }

func pushHub(duty string, r slog.Record) {
	var b strings.Builder
	first := true
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == dutyKey {
			return true
		}
		if !first {
			b.WriteByte(' ')
		}
		first = false
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
		return true
	})
	defaultHub.push(Line{
		Time:    r.Time,
		Level:   strings.ToLower(r.Level.String()),
		Duty:    duty,
		Message: r.Message,
		Attrs:   clipHubAttrs(b.String()),
	})
}

const maxHubAttrs = 4000

func clipHubAttrs(s string) string {
	r := []rune(s)
	if len(r) <= maxHubAttrs {
		return s
	}
	return string(r[:maxHubAttrs]) + "…(完整内容见 logs 目录文件)"
}

// ResetHubForTest 单测清空 ring，避免串扰
func ResetHubForTest() { defaultHub = newHub(defaultMemoryLines) }
