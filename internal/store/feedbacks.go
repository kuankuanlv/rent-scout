package store

import (
	"fmt"

	"rent-scout/internal/models"
)

// ListFeedbacks 反馈列表（/admin 报表），id 倒序限量
// TODO(m3): 死代码——预留的报表查询，当前无 API 对接；保留待后续 /admin/stats 接入或 v2 清理
func (s *Store) ListFeedbacks(limit int) ([]models.Feedback, error) {
	rows, err := s.db.Query(`SELECT id, post_id, channel, action, reason, created_at
	    FROM feedbacks ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("反馈列表: %w", err)
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
