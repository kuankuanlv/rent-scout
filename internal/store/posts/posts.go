package posts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// Repo 帖子领域数据访问
type Repo struct {
	DB *sql.DB
}

// InsertPost 去重入库：posts 表 UNIQUE(source, external_id)，
// 已存在则跳过并返回 added=false（帖子内容以首抓为准，避免通知重复，规格 4.6）
func (r *Repo) InsertPost(p models.RentPost) (bool, error) {
	if err := validatePostStatusWrite(p.Status); err != nil {
		return false, err
	}
	models.FillPostExtracted(&p)
	res, err := r.DB.Exec(`INSERT OR IGNORE INTO posts
	    (source, external_id, url, title, content, author, author_url, published_at, collected_at, status, raw, price, contact)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Source, p.ExternalID, p.URL, p.Title, p.Content, p.Author, p.AuthorURL,
		nullableTime(p.PublishedAt), p.CollectedAt, p.Status, p.Raw, p.Price, p.Contact)
	if err != nil {
		return false, fmt.Errorf("插入帖子: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

const fetchPostCols = `id, source, external_id, url, title, content, author, author_url,
	    published_at, collected_at, status, raw, price, contact`

func scanFetchPost(sc interface{ Scan(...any) error }) (models.RentPost, error) {
	var p models.RentPost
	var published sql.NullTime
	if err := sc.Scan(&p.ID, &p.Source, &p.ExternalID, &p.URL, &p.Title, &p.Content,
		&p.Author, &p.AuthorURL, &published, &p.CollectedAt, &p.Status, &p.Raw, &p.Price, &p.Contact); err != nil {
		return p, err
	}
	if published.Valid {
		p.PublishedAt = published.Time
	}
	return p, nil
}

// FetchPendingByStatus 拉取指定主状态的一批帖子，按 id 升序（先到先处理），限量。
func (r *Repo) FetchPendingByStatus(status string, limit int) ([]models.RentPost, error) {
	rows, err := r.DB.Query(`SELECT `+fetchPostCols+` FROM posts WHERE status = ? ORDER BY id LIMIT ?`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取 %s 批: %w", status, err)
	}
	defer rows.Close()
	var posts []models.RentPost
	for rows.Next() {
		p, err := scanFetchPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// FetchPassedWithoutAI 已通过硬规则、还没有 AI 结果的帖（AI 协程拉批）
func (r *Repo) FetchPassedWithoutAI(limit int) ([]models.RentPost, error) {
	rows, err := r.DB.Query(`SELECT p.id, p.source, p.external_id, p.url, p.title, p.content, p.author, p.author_url,
	    p.published_at, p.collected_at, p.status, p.raw, p.price, p.contact
	    FROM posts p
	    LEFT JOIN filter_results fr ON fr.post_id = p.id
	    WHERE p.status = 'passed' AND (fr.ai_result IS NULL OR fr.ai_result = '')
	    ORDER BY p.id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("拉取待 AI 审核: %w", err)
	}
	defer rows.Close()
	var posts []models.RentPost
	for rows.Next() {
		p, err := scanFetchPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// ListPublishedBetween 时间窗内已入库帖（规则 replay 用），按 id 升序限量
func (r *Repo) ListPublishedBetween(from, to time.Time, limit int) ([]models.RentPost, error) {
	if limit <= 0 {
		limit = 2000
	}
	rows, err := r.DB.Query(`SELECT `+fetchPostCols+` FROM posts
	    WHERE published_at >= ? AND published_at <= ? ORDER BY id LIMIT ?`, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("按发布时间拉帖: %w", err)
	}
	defer rows.Close()
	var posts []models.RentPost
	for rows.Next() {
		p, err := scanFetchPost(rows)
		if err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// UpdatePostPrice 把 AI 抽出的月租金写回 posts；yuan<=0 不改（保留正则结果或暂无）
func (r *Repo) UpdatePostPrice(postID int64, yuan int) error {
	if yuan <= 0 {
		return nil
	}
	if _, err := r.DB.Exec(`UPDATE posts SET price=? WHERE id=?`, models.FormatPriceYuan(yuan), postID); err != nil {
		return fmt.Errorf("更新帖子价格: %w", err)
	}
	return nil
}

// UpdatePostContact AI 抽出联系方式后写回；空或暂无不改
func (r *Repo) UpdatePostContact(postID int64, contact string) error {
	if !models.HasContact(contact) {
		return nil
	}
	if _, err := r.DB.Exec(`UPDATE posts SET contact=? WHERE id=?`, strings.TrimSpace(contact), postID); err != nil {
		return fmt.Errorf("更新帖子联系方式: %w", err)
	}
	return nil
}

// MarkStatus 原子更新一批帖子的主状态
func (r *Repo) MarkStatus(ids []int64, status string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := validatePostStatusWrite(status); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := r.DB.Exec(`UPDATE posts SET status = ? WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("批量状态流转 %s: %w", status, err)
	}
	return nil
}

func validatePostStatusWrite(status string) error {
	switch status {
	case models.PostStatusCollected, models.PostStatusPassed, models.PostStatusRejected:
		return nil
	case "sent", "acked", "pending":
		return fmt.Errorf("禁止写入已废弃帖子状态 %s", status)
	default:
		return fmt.Errorf("非法帖子状态 %s，仅允许 collected|passed|rejected", status)
	}
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
