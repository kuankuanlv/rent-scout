package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	ProgressBackfill    = "backfill"
	ProgressIncremental = "incremental"
)

// SourceProgress 源维度采集进度（JSON 存在 source_state.cursor，免改表）。
// backfill 用 Page 翻页；incremental 用 Watermark 水位，每轮从列表头抓新帖。
type SourceProgress struct {
	Phase     string `json:"phase"`
	Page      string `json:"page"`
	Watermark string `json:"watermark"`
	RangeKey  string `json:"range_key"`
}

// ParseSourceProgress 兼容旧纯字符串游标（当成 backfill 的 page）
func ParseSourceProgress(raw string) SourceProgress {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceProgress{Phase: ProgressBackfill}
	}
	if strings.HasPrefix(raw, "{") {
		var p SourceProgress
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			if p.Phase == "" {
				p.Phase = ProgressBackfill
			}
			return p
		}
	}
	return SourceProgress{Phase: ProgressBackfill, Page: raw}
}

func (p SourceProgress) Encode() string {
	if p.Phase == "" {
		p.Phase = ProgressBackfill
	}
	b, err := json.Marshal(p)
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
		return SourceProgress{Phase: ProgressBackfill}, ok, err
	}
	return ParseSourceProgress(raw), true, nil
}

// SetCursor 写入/更新源采集游标（增量断点续传，规格 3.5）
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
