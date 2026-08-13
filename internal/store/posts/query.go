package posts

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
func (r *Repo) ListPosts(f PostListFilter, limit, offset int) ([]models.RentPost, error) {
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

	rows, err := r.DB.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("帖子列表: %w", err)
	}
	defer rows.Close()
	out := make([]models.RentPost, 0)
	for rows.Next() {
		p, err := scanRentPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPost 单帖详情（/api/posts/{id}）；不存在返回 ok=false
func (r *Repo) GetPost(id int64) (models.RentPost, bool, error) {
	row := r.DB.QueryRow(`SELECT id, source, external_id, url, title, content, author, author_url,
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
func (r *Repo) MarkPostHandled(postID int64) error {
	if _, err := r.DB.Exec(`UPDATE posts SET handled_at=? WHERE id=?`, time.Now(), postID); err != nil {
		return fmt.Errorf("标记已处理: %w", err)
	}
	return nil
}

// ClearPostHandled 清 handled_at=NULL（不改 status / 反馈）
func (r *Repo) ClearPostHandled(postID int64) error {
	if _, err := r.DB.Exec(`UPDATE posts SET handled_at=NULL WHERE id=?`, postID); err != nil {
		return fmt.Errorf("清除已处理: %w", err)
	}
	return nil
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
