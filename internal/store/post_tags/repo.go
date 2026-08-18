package posttags

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"rent-scout/internal/models"
)

// Repo post_tags 表访问
type Repo struct {
	DB *sql.DB
}

// ReplaceSystemTags 硬规则定案/replay：删该帖全部 system 行后重写
func (r *Repo) ReplaceSystemTags(postID int64, tags []models.PostTag) error {
	tx, err := r.DB.Begin()
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM post_tags WHERE post_id=? AND source=?`, postID, models.TagSourceSystem); err != nil {
		return fmt.Errorf("清系统标签: %w", err)
	}
	now := time.Now()
	for _, t := range tags {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, ?)`,
			postID, t.Kind, strings.TrimSpace(t.Text), models.TagSourceSystem, now); err != nil {
			return fmt.Errorf("写系统标签: %w", err)
		}
	}
	return tx.Commit()
}

// AddUserTags 人工标记：追加 user 行，不覆盖历史
func (r *Repo) AddUserTags(postID int64, tags []models.PostTag) error {
	now := time.Now()
	for _, t := range tags {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		if _, err := r.DB.Exec(`INSERT INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, ?)`,
			postID, t.Kind, strings.TrimSpace(t.Text), models.TagSourceUser, now); err != nil {
			return fmt.Errorf("写用户标签: %w", err)
		}
	}
	return nil
}

// ListByPostID 单帖全部标签，按 id 升序
func (r *Repo) ListByPostID(postID int64) ([]models.PostTag, error) {
	rows, err := r.DB.Query(`SELECT id, post_id, kind, text, source, created_at FROM post_tags WHERE post_id=? ORDER BY id`, postID)
	if err != nil {
		return nil, fmt.Errorf("查帖子标签: %w", err)
	}
	defer rows.Close()
	return scanTags(rows)
}

// ListByPostIDs 批量查标签，按 post_id、id 升序
func (r *Repo) ListByPostIDs(postIDs []int64) (map[int64][]models.PostTag, error) {
	out := make(map[int64][]models.PostTag)
	if len(postIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(postIDs)), ",")
	args := make([]any, len(postIDs))
	for i, id := range postIDs {
		args[i] = id
	}
	rows, err := r.DB.Query(`SELECT id, post_id, kind, text, source, created_at FROM post_tags WHERE post_id IN (`+ph+`) ORDER BY post_id, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("批量查标签: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out[t.PostID] = append(out[t.PostID], t)
	}
	return out, rows.Err()
}

// ListDistinctTexts 标签下拉：全部 text 按出现帖数降序
func (r *Repo) ListDistinctTexts() ([]string, error) {
	rows, err := r.DB.Query(`SELECT text, COUNT(DISTINCT post_id) AS n FROM post_tags GROUP BY text ORDER BY n DESC, text COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("聚合标签: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		var n int
		if err := rows.Scan(&text, &n); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

func scanTags(rows *sql.Rows) ([]models.PostTag, error) {
	var out []models.PostTag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []models.PostTag{}
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTag(sc rowScanner) (models.PostTag, error) {
	var t models.PostTag
	if err := sc.Scan(&t.ID, &t.PostID, &t.Kind, &t.Text, &t.Source, &t.CreatedAt); err != nil {
		return t, err
	}
	return t, nil
}

// SortTags 展示顺序：system 在前，同 source 按 id
func SortTags(tags []models.PostTag) {
	sort.SliceStable(tags, func(i, j int) bool {
		if tags[i].Source != tags[j].Source {
			return tags[i].Source == models.TagSourceSystem
		}
		return tags[i].ID < tags[j].ID
	})
}
