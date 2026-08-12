package store

import (
	"database/sql"
	"fmt"

	"rent-scout/internal/models"
)

// FetchDeadNotifications 死信列表（/admin 人工复查重发，规格 6.6），id 倒序限量
func (s *Store) FetchDeadNotifications(limit int) ([]models.Notification, error) {
	rows, err := s.db.Query(`SELECT id, post_id, channel, status, attempts, last_error, sent_at
	    FROM notifications WHERE status='dead' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取死信列表: %w", err)
	}
	defer rows.Close()
	var items []models.Notification
	for rows.Next() {
		var n models.Notification
		var sent sql.NullTime
		if err := rows.Scan(&n.ID, &n.PostID, &n.Channel, &n.Status, &n.Attempts, &n.LastError, &sent); err != nil {
			return nil, err
		}
		if sent.Valid {
			n.SentAt = &sent.Time
		}
		items = append(items, n)
	}
	return items, rows.Err()
}

// ResetNotification 死信重发：dead → pending，attempts=0、last_error=”。
// 下轮 FetchNotifyBatch 自动捞回重发；幂等：非 dead 状态返回 false 不报错
func (s *Store) ResetNotification(postID int64, channel string) (bool, error) {
	res, err := s.db.Exec(`UPDATE notifications SET status='pending', attempts=0, last_error=''
	    WHERE post_id=? AND channel=? AND status='dead'`, postID, channel)
	if err != nil {
		return false, fmt.Errorf("重置死信: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
