package notify

import (
	"database/sql"
	"fmt"
	"time"

	"rent-scout/internal/models"
)

// Repo 通知/反馈领域数据访问
type Repo struct {
	DB *sql.DB
}

// InsertNotification 插入渠道通知记录；UNIQUE(post_id, channel) 保证幂等，已存在返回 false
func (r *Repo) InsertNotification(postID int64, channel string) (bool, error) {
	res, err := r.DB.Exec(`INSERT OR IGNORE INTO notifications (post_id, channel, status) VALUES (?, ?, 'pending')`,
		postID, channel)
	if err != nil {
		return false, fmt.Errorf("插入通知: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FetchPendingNotifications 拉取指定渠道待发送/可重试的通知（pending 或 failed），限量
func (r *Repo) FetchPendingNotifications(channel string, limit int) ([]models.Notification, error) {
	rows, err := r.DB.Query(`SELECT id, post_id, channel, status, attempts, last_error, sent_at
	    FROM notifications WHERE channel=? AND status IN ('pending','failed') ORDER BY id LIMIT ?`, channel, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取待通知: %w", err)
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

// MarkNotificationSent 发送成功：置 sent 并记录时间
func (r *Repo) MarkNotificationSent(postID int64, channel string) error {
	_, err := r.DB.Exec(`UPDATE notifications SET status='sent', sent_at=? WHERE post_id=? AND channel=?`,
		time.Now(), postID, channel)
	if err != nil {
		return fmt.Errorf("标记已通知: %w", err)
	}
	return nil
}

// MarkNotificationFailed 发送失败：记录错误与重试次数；attempts 超过阈值由调用方置 dead
func (r *Repo) MarkNotificationFailed(postID int64, channel, errMsg string, attempts int) error {
	_, err := r.DB.Exec(`UPDATE notifications SET status='failed', last_error=?, attempts=? WHERE post_id=? AND channel=?`,
		errMsg, attempts, postID, channel)
	if err != nil {
		return fmt.Errorf("标记通知失败: %w", err)
	}
	return nil
}

// MarkNotificationDead 死信：超过重试阈值，人工在 /admin 重发
func (r *Repo) MarkNotificationDead(postID int64, channel, errMsg string) error {
	_, err := r.DB.Exec(`UPDATE notifications SET status='dead', last_error=? WHERE post_id=? AND channel=?`,
		errMsg, postID, channel)
	if err != nil {
		return fmt.Errorf("标记死信: %w", err)
	}
	return nil
}

// NotificationAttempts 查询渠道通知的当前尝试次数；无记录返回 0
func (r *Repo) NotificationAttempts(postID int64, channel string) (int, error) {
	var attempts int
	err := r.DB.QueryRow(`SELECT attempts FROM notifications WHERE post_id=? AND channel=?`,
		postID, channel).Scan(&attempts)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("查询尝试次数: %w", err)
	}
	return attempts, nil
}

// InsertFeedback 写入用户反馈（规格 5.5 自学习闭环入口）
func (r *Repo) InsertFeedback(f models.Feedback) error {
	_, err := r.DB.Exec(`INSERT INTO feedbacks (post_id, channel, action, reason, created_at) VALUES (?, ?, ?, ?, ?)`,
		f.PostID, f.Channel, f.Action, f.Reason, time.Now())
	if err != nil {
		return fmt.Errorf("写反馈: %w", err)
	}
	return nil
}

// ListNotificationsByPost 帖子的全部通知记录（详情页展示），按 id 升序
func (r *Repo) ListNotificationsByPost(postID int64) ([]models.Notification, error) {
	rows, err := r.DB.Query(`SELECT id, post_id, channel, status, attempts, last_error, sent_at
	    FROM notifications WHERE post_id=? ORDER BY id`, postID)
	if err != nil {
		return nil, fmt.Errorf("查帖子通知: %w", err)
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

// ListFeedbacksByPost 帖子的反馈记录（详情页展示），按 id 升序
func (r *Repo) ListFeedbacksByPost(postID int64) ([]models.Feedback, error) {
	rows, err := r.DB.Query(`SELECT id, post_id, channel, action, reason, created_at
	    FROM feedbacks WHERE post_id=? ORDER BY id`, postID)
	if err != nil {
		return nil, fmt.Errorf("查帖子反馈: %w", err)
	}
	defer rows.Close()
	var items []models.Feedback
	for rows.Next() {
		var f models.Feedback
		if err := rows.Scan(&f.ID, &f.PostID, &f.Channel, &f.Action, &f.Reason, &f.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}
