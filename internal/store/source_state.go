package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SourceProgress 每个 source 一份采集进度（JSON 存在 source_state.cursor）。
// Fingerprint：source 配置身份（相对起点 + 小组等），不含 now。
// Page：还在往旧帖翻时的页游标；空且 SeenNewest 非空 = 已翻完历史、从首页追新。
type SourceProgress struct {
	Fingerprint string `json:"fp"`
	Page        string `json:"page"`
	SeenNewest  string `json:"seen_newest"`
}

// ParseSourceProgress 解析 JSON 进度；非 JSON 或空串视为无进度
func ParseSourceProgress(raw string) SourceProgress {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return SourceProgress{}
	}
	var p SourceProgress
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return SourceProgress{}
	}
	return p
}

// DecodeWatermarks 把 seen_newest 解成 目标键→时间（JSON 对象）
func DecodeWatermarks(seen string) map[string]string {
	seen = strings.TrimSpace(seen)
	if seen == "" || !strings.HasPrefix(seen, "{") {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(seen), &m); err != nil || m == nil {
		return map[string]string{}
	}
	return m
}

func EncodeWatermarks(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func LookupWatermark(m map[string]string, key string) time.Time {
	if key == "" || len(m) == 0 {
		return time.Time{}
	}
	if v, ok := m[key]; ok {
		return parseStoredWatermark(v)
	}
	return time.Time{}
}

func parseStoredWatermark(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// CatchingUp 历史已翻完，本轮从列表头追新
func (p SourceProgress) CatchingUp() bool {
	return strings.TrimSpace(p.Page) == "" && strings.TrimSpace(p.SeenNewest) != ""
}

func (p SourceProgress) Encode() string {
	b, err := json.Marshal(struct {
		Fingerprint string `json:"fp"`
		Page        string `json:"page"`
		SeenNewest  string `json:"seen_newest"`
	}{p.Fingerprint, p.Page, p.SeenNewest})
	if err != nil {
		return ""
	}
	return string(b)
}

// GetCursor 读取源采集游标原文；ok=false 表示该源尚无游标（首次采集）
func (s *Store) GetCursor(source string) (value string, ok bool, err error) {
	var updated time.Time
	err = s.db.QueryRow(`SELECT cursor, updated_at FROM source_state WHERE source=?`, source).Scan(&value, &updated)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读游标 %s: %w", source, err)
	}
	return value, true, nil
}

// GetProgress 读源进度；无记录时 ok=false
func (s *Store) GetProgress(source string) (SourceProgress, bool, error) {
	raw, ok, err := s.GetCursor(source)
	if err != nil || !ok {
		return SourceProgress{}, ok, err
	}
	return ParseSourceProgress(raw), true, nil
}

// SetCursor 写入/更新源采集游标
func (s *Store) SetCursor(source, value string) error {
	_, err := s.db.Exec(`INSERT INTO source_state (source, cursor, updated_at) VALUES (?, ?, ?)
	    ON CONFLICT(source) DO UPDATE SET cursor=excluded.cursor, updated_at=excluded.updated_at`,
		source, value, time.Now())
	if err != nil {
		return fmt.Errorf("写游标 %s: %w", source, err)
	}
	return nil
}

// SetProgress 写结构化进度
func (s *Store) SetProgress(source string, p SourceProgress) error {
	return s.SetCursor(source, p.Encode())
}

// ClearProgress 重置源进度（改时间窗/小组后或手动重置）
func (s *Store) ClearProgress(source string) error {
	_, err := s.db.Exec(`DELETE FROM source_state WHERE source=?`, source)
	if err != nil {
		return fmt.Errorf("清游标 %s: %w", source, err)
	}
	return nil
}
