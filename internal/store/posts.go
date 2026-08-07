package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// InsertPost 去重入库：posts 表 UNIQUE(source, external_id)，
// 已存在则跳过并返回 added=false（帖子内容以首抓为准，避免通知重复，规格 4.6）
func (s *Store) InsertPost(p models.RentPost) (bool, error) {
	// nil 标签序列化为 "[]" 而非 "null"（与列默认值 '[]' 一致）
	tags := p.AddressTags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return false, fmt.Errorf("序列化地址标签: %w", err)
	}
	// INSERT OR IGNORE：UNIQUE 冲突静默跳过，RowsAffected=0 → added=false
	res, err := s.db.Exec(`INSERT OR IGNORE INTO posts
	    (source, external_id, url, title, content, author, author_url, published_at, collected_at, status, address_tags, raw)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Source, p.ExternalID, p.URL, p.Title, p.Content, p.Author, p.AuthorURL,
		nullableTime(p.PublishedAt), p.CollectedAt, p.Status, string(tagsJSON), p.Raw)
	if err != nil {
		return false, fmt.Errorf("插入帖子: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// FetchPendingByStatus 拉取指定主状态的一批帖子，按 id 升序（先到先处理），限量。
// 模块消费协议组批入口（规格 2.3）
func (s *Store) FetchPendingByStatus(status string, limit int) ([]models.RentPost, error) {
	rows, err := s.db.Query(`SELECT id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, address_tags, raw FROM posts WHERE status = ? ORDER BY id LIMIT ?`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取 %s 批: %w", status, err)
	}
	defer rows.Close()
	var posts []models.RentPost
	for rows.Next() {
		var p models.RentPost
		var published sql.NullTime
		var tagsJSON string
		if err := rows.Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
			&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &tagsJSON, &p.Raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &p.AddressTags); err != nil {
			return nil, fmt.Errorf("解析地址标签: %w", err)
		}
		if published.Valid {
			p.PublishedAt = published.Time
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// MarkStatus 原子更新一批帖子的主状态（状态机流转，规格 2.4）
func (s *Store) MarkStatus(ids []int64, status string) error {
	if len(ids) == 0 {
		return nil
	}
	// 构造占位符列表，批量 IN 更新
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := s.db.Exec(`UPDATE posts SET status = ? WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("批量状态流转 %s: %w", status, err)
	}
	return nil
}

// nullableTime time.Time 零值写 NULL（published_at 可空）
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
