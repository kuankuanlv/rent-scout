package store

import (
	"database/sql"
	"time"
)

const setupCompletedKey = "setup.completed"


// ConfigEntry 配置项
type ConfigEntry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ConfigHistoryEntry 配置历史
type ConfigHistoryEntry struct {
	ID        int64
	Key       string
	OldValue  string
	NewValue  string
	CreatedAt time.Time
}

// GetConfigMap 读取全部 KV（供 HotConfig 使用）
func GetConfigMap(s *Store) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM kv_config ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ConfigCount 配置项数量
func ConfigCount(s *Store) (int, error) {
	var cnt int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM kv_config`).Scan(&cnt)
	return cnt, err
}

// IsSetupComplete 是否已完成首次引导
func IsSetupComplete(s *Store) bool {
	v, err := GetConfig(s, setupCompletedKey)
	return err == nil && v == "true"
}

// GetAllConfig 读取所有配置（admin 展示用，secret 值打码）
func GetAllConfig(s *Store) ([]ConfigEntry, error) {
	rows, err := s.db.Query(`SELECT key, value, updated_at FROM kv_config ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ConfigEntry
	for rows.Next() {
		var e ConfigEntry
		var updatedAt int64
		if err := rows.Scan(&e.Key, &e.Value, &updatedAt); err != nil {
			return nil, err
		}
		e.UpdatedAt = time.Unix(updatedAt, 0)
		if isSecretKey(e.Key) && e.Value != "" {
			e.Value = "••••••••"
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func isSecretKey(key string) bool {
	return len(key) > 7 && key[:7] == "secret."
}

// GetConfig 读取单个配置
func GetConfig(s *Store, key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM kv_config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetConfig 设置单个配置（UPSERT）并记录历史
func SetConfig(s *Store, key, value string) error {
	old, _ := GetConfig(s, key)
	if err := setConfigRaw(s, key, value); err != nil {
		return err
	}
	if old != value {
		return RecordConfigHistory(s, key, old, value)
	}
	return nil
}

func setConfigRaw(s *Store, key, value string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO kv_config (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, now)
	return err
}

// SetConfigBatch 批量设置配置并记录历史
func SetConfigBatch(s *Store, updates map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	upsert, err := tx.Prepare(`
		INSERT INTO kv_config (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer upsert.Close()

	hist, err := tx.Prepare(`
		INSERT INTO config_history (key, old_value, new_value, created_at)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer hist.Close()

	for key, value := range updates {
		var old string
		_ = tx.QueryRow(`SELECT value FROM kv_config WHERE key = ?`, key).Scan(&old)
		if _, err := upsert.Exec(key, value, now); err != nil {
			return err
		}
		if old != value {
			if _, err := hist.Exec(key, old, value, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ListConfigHistory 列出配置历史
func ListConfigHistory(s *Store, limit int) ([]ConfigHistoryEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, key, old_value, new_value, created_at
		FROM config_history
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []ConfigHistoryEntry
	for rows.Next() {
		var e ConfigHistoryEntry
		var createdAt int64
		if err := rows.Scan(&e.ID, &e.Key, &e.OldValue, &e.NewValue, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(createdAt, 0)
		if isSecretKey(e.Key) {
			if e.OldValue != "" {
				e.OldValue = "••••"
			}
			if e.NewValue != "" {
				e.NewValue = "••••"
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// RecordConfigHistory 记录配置变更历史
func RecordConfigHistory(s *Store, key, oldVal, newVal string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO config_history (key, old_value, new_value, created_at)
		VALUES (?, ?, ?, ?)
	`, key, oldVal, newVal, now)
	return err
}

// MarkSetupComplete 标记首次引导完成
func MarkSetupComplete(s *Store) error {
	return SetConfig(s, setupCompletedKey, "true")
}
