package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/models"
)

// FetchNotifyBatch 拉取 passed 且对任一启用渠道未发送（无记录或 pending/failed）的帖子。
// 语义（规格 6.5）：批内帖子至少有一个渠道待发；已全渠道 sent 的帖子不再返回。
// 实现：已 sent 渠道数 < 启用渠道数（notifications 表 UNIQUE(post_id, channel)，每帖每渠道至多一条）
// channels 为空时返回空批（防御，调用方恒传启用渠道列表）
func (s *Store) FetchNotifyBatch(channels []string, limit int) ([]models.RentPost, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	ph := strings.Repeat("?,", len(channels))
	ph = ph[:len(ph)-1]
	query := fmt.Sprintf(`
		SELECT id, source, external_id, url, title, content, author, author_url,
		       published_at, collected_at, status, address_tags, raw
		FROM posts
		WHERE status = 'passed'
		  AND (SELECT COUNT(*) FROM notifications n
		       WHERE n.post_id = posts.id AND n.channel IN (%s) AND n.status = 'sent') < ?
		ORDER BY id
		LIMIT ?`, ph)
	args := make([]any, 0, len(channels)+2)
	for _, c := range channels {
		args = append(args, c)
	}
	args = append(args, len(channels), limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("拉取待通知批: %w", err)
	}
	defer rows.Close()
	var items []models.RentPost
	for rows.Next() {
		var p models.RentPost
		var published sql.NullTime
		var tagsJSON string
		if err := rows.Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
			&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &tagsJSON, &p.Raw); err != nil {
			return nil, err
		}
		if published.Valid {
			p.PublishedAt = published.Time
		}
		if err := json.Unmarshal([]byte(tagsJSON), &p.AddressTags); err != nil {
			return nil, fmt.Errorf("解析地址标签: %w", err)
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// NotificationStatuses 批量查询帖子的渠道通知状态：返回 map[postID]map[channel]status。
// 无记录的渠道不出现（调用方视为未发送）
func (s *Store) NotificationStatuses(postIDs []int64, channels []string) (map[int64]map[string]string, error) {
	out := make(map[int64]map[string]string)
	if len(postIDs) == 0 || len(channels) == 0 {
		return out, nil
	}
	postPh := strings.Repeat("?,", len(postIDs))
	postPh = postPh[:len(postPh)-1]
	chanPh := strings.Repeat("?,", len(channels))
	chanPh = chanPh[:len(chanPh)-1]
	query := fmt.Sprintf(
		`SELECT post_id, channel, status FROM notifications
		 WHERE post_id IN (%s) AND channel IN (%s)`, postPh, chanPh)
	args := make([]any, 0, len(postIDs)+len(channels))
	for _, id := range postIDs {
		args = append(args, id)
	}
	for _, c := range channels {
		args = append(args, c)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询通知状态: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var postID int64
		var channel, status string
		if err := rows.Scan(&postID, &channel, &status); err != nil {
			return nil, err
		}
		if out[postID] == nil {
			out[postID] = map[string]string{}
		}
		out[postID][channel] = status
	}
	return out, rows.Err()
}
