package posts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// PostListFilter 帖子列表筛选（规格 §6）：空字段 = 不限
type PostListFilter struct {
	Q       string // title/content LIKE
	Status  string
	Tag     string // post_tags.text 精确匹配
	Handled string // "0"=NULL，"1"=非空，其它/空=不限
	AI      string // reviewed / unreviewed / pass / fail（独立于 Tag）
	Source  string // douban / weibo；空=不限
}

func postListWhere(f PostListFilter) (string, []any) {
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
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if tags := models.SplitFilterTags(f.Tag); len(tags) > 0 {
		// 多选按或：帖子带其中任意一个标签就算命中
		ph := strings.TrimSuffix(strings.Repeat("?,", len(tags)), ",")
		where = append(where, `EXISTS (SELECT 1 FROM post_tags t WHERE t.post_id = posts.id AND t.text IN (`+ph+`))`)
		for _, t := range tags {
			args = append(args, t)
		}
	}
	switch f.Handled {
	case "0":
		where = append(where, "handled_at IS NULL")
	case "1":
		where = append(where, "handled_at IS NOT NULL")
	}
	switch f.AI {
	case "reviewed":
		where = append(where, `EXISTS (SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id AND fr.ai_result != '')`)
	case "unreviewed":
		where = append(where, `NOT EXISTS (SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id AND fr.ai_result != '')`)
	case "pass":
		where = append(where, `EXISTS (SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id AND CASE WHEN fr.ai_result != '' THEN json_extract(fr.ai_result, '$.passed') END = 1)`)
	case "fail":
		where = append(where, `EXISTS (SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id AND CASE WHEN fr.ai_result != '' THEN json_extract(fr.ai_result, '$.passed') END = 0)`)
	}
	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

const postSelectCols = `id, source, external_id, url, title, content, author, author_url,
			    published_at, collected_at, status, handled_at, raw, price, contact`

// ListPosts 帖子列表；按发布时间倒序，空发布时间再按采集时间、id
func (r *Repo) ListPosts(f PostListFilter, limit, offset int) ([]models.RentPost, error) {
	clause, args := postListWhere(f)
	sqlStr := `SELECT ` + postSelectCols + ` FROM posts` + clause +
		` ORDER BY COALESCE(published_at, collected_at) DESC, id DESC LIMIT ? OFFSET ?`
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

// CountPosts 与 ListPosts 同一套筛选
func (r *Repo) CountPosts(f PostListFilter) (int, error) {
	clause, args := postListWhere(f)
	var n int
	err := r.DB.QueryRow(`SELECT COUNT(*) FROM posts`+clause, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("帖子计数: %w", err)
	}
	return n, nil
}

// GetPost 单帖详情（/api/posts/{id}）；不存在返回 ok=false
func (r *Repo) GetPost(id int64) (models.RentPost, bool, error) {
	row := r.DB.QueryRow(`SELECT `+postSelectCols+` FROM posts WHERE id=?`, id)
	p, err := scanRentPost(row)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, fmt.Errorf("查帖子详情: %w", err)
	}
	return p, true, nil
}

// MarkPostHandled 写 handled_at=现在（不改 status / 标签）
func (r *Repo) MarkPostHandled(postID int64) error {
	if _, err := r.DB.Exec(`UPDATE posts SET handled_at=? WHERE id=?`, time.Now(), postID); err != nil {
		return fmt.Errorf("标记已处理: %w", err)
	}
	return nil
}

// ClearPostHandled 清 handled_at=NULL（不改 status / 标签）
func (r *Repo) ClearPostHandled(postID int64) error {
	if _, err := r.DB.Exec(`UPDATE posts SET handled_at=NULL WHERE id=?`, postID); err != nil {
		return fmt.Errorf("清除已处理: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRentPost(sc rowScanner) (models.RentPost, error) {
	var p models.RentPost
	var published, handled sql.NullTime
	if err := sc.Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
		&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &handled, &p.Raw, &p.Price, &p.Contact); err != nil {
		return p, err
	}
	if published.Valid {
		p.PublishedAt = published.Time
	}
	if handled.Valid {
		t := handled.Time
		p.HandledAt = &t
	}
	return p, nil
}

// ListPostsByIDs 按 id 拉帖，顺序跟传入一致；找不到的跳过
func (r *Repo) ListPostsByIDs(ids []int64) ([]models.RentPost, error) {
	seen := map[int64]bool{}
	var uniq []int64
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(uniq))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(uniq))
	for i, id := range uniq {
		args[i] = id
	}
	rows, err := r.DB.Query(`SELECT `+postSelectCols+` FROM posts WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("按 id 拉帖: %w", err)
	}
	defer rows.Close()
	byID := make(map[int64]models.RentPost, len(uniq))
	for rows.Next() {
		p, err := scanRentPost(rows)
		if err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]models.RentPost, 0, len(uniq))
	for _, id := range uniq {
		if p, ok := byID[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}
