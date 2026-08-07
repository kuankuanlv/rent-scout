package store

import (
	"database/sql"
	"fmt"
	"time"
)

// GetCursor 读取源采集游标；ok=false 表示该源尚无游标（首次采集）
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
