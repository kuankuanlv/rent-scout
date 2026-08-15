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
	Tag     string // 硬规则标签（白/黑/默认拒绝）；不含 AI
	Handled string // "0"=NULL，"1"=非空，其它/空=不限
	AI      string // reviewed / unreviewed / pass / fail（独立于 Tag）
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
	if f.Tag != "" {
		// 硬规则标签：白名单地点 / 黑名单词 / 默认拒绝（AI 走独立 ai 条件）
		tag := f.Tag
		like := "%" + tag + "%"
		where = append(where, `(
			posts.address_tags LIKE ?
			OR EXISTS (
				SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id AND (
					fr.hard_rules LIKE ?
					OR (fr.rejected_by = '默认拒绝' AND ? = '默认拒绝')
				)
			)
			OR (? = '默认拒绝' AND posts.status = 'rejected'
				AND NOT EXISTS (SELECT 1 FROM filter_results fr WHERE fr.post_id = posts.id))
		)`)
		args = append(args, like, like, tag, tag)
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

// ListPosts 帖子列表；按发布时间倒序，空发布时间再按采集时间、id
// 不用 datetime()：库里是 Go time.String()（带时区），datetime 解成 NULL 会退化成 id 排序
func (r *Repo) ListPosts(f PostListFilter, limit, offset int) ([]models.RentPost, error) {
	clause, args := postListWhere(f)
		sqlStr := `SELECT id, source, external_id, url, title, content, author, author_url,
			    published_at, collected_at, status, address_tags, handled_at, raw, price, contact FROM posts` + clause +
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
		row := r.DB.QueryRow(`SELECT id, source, external_id, url, title, content, author, author_url,
		    published_at, collected_at, status, address_tags, handled_at, raw, price, contact FROM posts WHERE id=?`, id)
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
			&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &tagsJSON, &handled, &p.Raw, &p.Price, &p.Contact); err != nil {
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
