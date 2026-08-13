package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// PostListFilter 帖子列表筛选（规格 §6）：空字段 = 不限
type PostListFilter struct {
	Q       string // title/content LIKE
	Status  string
	Tag     string // address_tags 文本包含
	Handled string // "0"=NULL，"1"=非空，其它/空=不限
}

// ListPosts 帖子列表（/api/posts，规格 7.1 + §6）；id 倒序分页
func (s *Store) ListPosts(f PostListFilter, limit, offset int) ([]models.RentPost, error) {
	var where []string
	var args []any
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Q != "" {
		where = append(where, "(title LIKE ? OR content LIKE ?)")
		like := "%" + f.Q + "%"
		args = append(args, like, like)
	}
	if f.Tag != "" {
		where = append(where, "address_tags LIKE ?")
		args = append(args, "%"+f.Tag+"%")
	}
	switch f.Handled {
	case "0":
		where = append(where, "handled_at IS NULL")
	case "1":
		where = append(where, "handled_at IS NOT NULL")
	}
	sqlStr := `SELECT id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, address_tags, handled_at, raw FROM posts`
	if len(where) > 0 {
		sqlStr += " WHERE " + strings.Join(where, " AND ")
	}
	sqlStr += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("帖子列表: %w", err)
	}
	defer rows.Close()
	posts := make([]models.RentPost, 0)
	for rows.Next() {
		p, err := scanRentPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// GetPost 单帖详情（/api/posts/{id}）；不存在返回 ok=false
func (s *Store) GetPost(id int64) (models.RentPost, bool, error) {
	row := s.db.QueryRow(`SELECT id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, address_tags, handled_at, raw FROM posts WHERE id=?`, id)
	p, err := scanRentPost(row)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("查帖子详情: %w", err)
	}
	return p, true, nil
}

// MarkPostHandled 写 handled_at=现在（不改 status / 反馈）
func (s *Store) MarkPostHandled(postID int64) error {
	if _, err := s.db.Exec(`UPDATE posts SET handled_at=? WHERE id=?`, time.Now(), postID); err != nil {
		return fmt.Errorf("标记已处理: %w", err)
	}
	return nil
}

// ClearPostHandled 清 handled_at=NULL（不改 status / 反馈）
func (s *Store) ClearPostHandled(postID int64) error {
	if _, err := s.db.Exec(`UPDATE posts SET handled_at=NULL WHERE id=?`, postID); err != nil {
		return fmt.Errorf("清除已处理: %w", err)
	}
	return nil
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

// rowScanner 统一 QueryRow / Rows 的 Scan
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRentPost(sc rowScanner) (models.RentPost, error) {
	var p models.RentPost
	var published, handled sql.NullTime
	var tagsJSON string
	if err := sc.Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
		&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &tagsJSON, &handled, &p.Raw); err != nil {
		return p, err
	}
	if published.Valid {
		p.PublishedAt = published.Time
	}
	if handled.Valid {
		t := handled.Time
		p.HandledAt = &t
	}
	if err := json.Unmarshal([]byte(tagsJSON), &p.AddressTags); err != nil {
		return p, fmt.Errorf("解析地址标签: %w", err)
	}
	return p, nil
}
