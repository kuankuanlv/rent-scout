package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"rent-scout/internal/models"
)

// ListPosts 帖子列表（/api/posts，规格 7.1）；status 空 = 全部；id 倒序分页
func (s *Store) ListPosts(status string, limit, offset int) ([]models.RentPost, error) {
	rows, err := s.db.Query(`SELECT id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, address_tags, raw FROM posts
	    WHERE (? = '' OR status = ?) ORDER BY id DESC LIMIT ? OFFSET ?`,
		status, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("帖子列表: %w", err)
	}
	defer rows.Close()
	posts := make([]models.RentPost, 0)
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
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// GetPost 单帖详情（/api/posts/{id}）；不存在返回 ok=false
func (s *Store) GetPost(id int64) (models.RentPost, bool, error) {
	var p models.RentPost
	var published sql.NullTime
	var tagsJSON string
	err := s.db.QueryRow(`SELECT id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, address_tags, raw FROM posts WHERE id=?`, id).
		Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
			&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &tagsJSON, &p.Raw)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("查帖子详情: %w", err)
	}
	if published.Valid {
		p.PublishedAt = published.Time
	}
	if err := json.Unmarshal([]byte(tagsJSON), &p.AddressTags); err != nil {
		return p, false, fmt.Errorf("解析地址标签: %w", err)
	}
	return p, true, nil
}

// ListNotificationsByPost 帖子的全部通知记录（详情页展示），按 id 升序
func (s *Store) ListNotificationsByPost(postID int64) ([]models.Notification, error) {
	rows, err := s.db.Query(`SELECT id, post_id, channel, status, attempts, last_error, sent_at
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
func (s *Store) ListFeedbacksByPost(postID int64) ([]models.Feedback, error) {
	rows, err := s.db.Query(`SELECT id, post_id, channel, action, reason, created_at
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
