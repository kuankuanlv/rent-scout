// 一次性：旧库 address_tags / filter_results / feedbacks → post_tags，再删旧列/表。
// 用法：go run ./cmd/migrate-post-tags [-db db/rent-scout.db]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rent-scout/internal/models"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "db/rent-scout.db", "SQLite 库路径")
	dryRun := flag.Bool("dry-run", false, "只统计不写库")
	flag.Parse()

	abs, err := filepath.Abs(*dbPath)
	if err != nil {
		fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		fatal(fmt.Errorf("库不存在: %s", abs))
	}

	if !*dryRun {
		bak := abs + ".bak-" + time.Now().Format("20060102-150405")
		if err := backupDB(abs, bak); err != nil {
			fatal(fmt.Errorf("备份失败: %w", err))
		}
		fmt.Println("已备份 →", bak)
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		fatal(err)
	}

	stats, err := plan(db)
	if err != nil {
		fatal(err)
	}
	printPlan(stats)

	if *dryRun {
		fmt.Println("dry-run 结束，未改库")
		return
	}

	if err := migrate(db, stats); err != nil {
		fatal(err)
	}
	fmt.Println("迁移完成")
}

type planStats struct {
	hasPostTags    bool
	hasAddressTags bool
	hasFeedbacks   bool
	existingTags   int
	locRows        int
	blockRows      int
	unmatchedRows  int
	feedbackRows   int
	manualRows     int
}

func plan(db *sql.DB) (planStats, error) {
	var st planStats
	st.hasPostTags = tableExists(db, "post_tags")
	st.hasAddressTags = columnExists(db, "posts", "address_tags")
	st.hasFeedbacks = tableExists(db, "feedbacks")
	if st.hasPostTags {
		_ = db.QueryRow(`SELECT COUNT(*) FROM post_tags`).Scan(&st.existingTags)
	}
	if st.hasAddressTags {
		_ = db.QueryRow(`SELECT COUNT(*) FROM posts WHERE address_tags IS NOT NULL AND address_tags != '' AND address_tags != '[]'`).Scan(&st.locRows)
	}
	if st.hasFeedbacks {
		_ = db.QueryRow(`SELECT COUNT(*) FROM feedbacks`).Scan(&st.feedbackRows)
		_ = db.QueryRow(`SELECT COUNT(*) FROM feedbacks WHERE trim(reason) != ''`).Scan(&st.manualRows)
	}
	// block / unmatched 粗算：扫 filter_results
	rows, err := db.Query(`SELECT p.status, fr.status, fr.hard_rules
	    FROM posts p LEFT JOIN filter_results fr ON fr.post_id = p.id`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var postStatus string
		var frStatus, hardJSON sql.NullString
		if err := rows.Scan(&postStatus, &frStatus, &hardJSON); err != nil {
			return st, err
		}
		if !frStatus.Valid {
			if postStatus == models.PostStatusRejected {
				st.unmatchedRows++
			}
			continue
		}
		var hard []models.RuleHit
		if hardJSON.Valid && hardJSON.String != "" {
			_ = json.Unmarshal([]byte(hardJSON.String), &hard)
		}
		if frStatus.String == models.PostStatusRejected {
			if len(hard) > 0 {
				st.blockRows += len(hard)
			} else if postStatus == models.PostStatusRejected {
				st.unmatchedRows++
			}
		}
	}
	return st, rows.Err()
}

func printPlan(st planStats) {
	fmt.Printf("post_tags 表: %v（已有 %d 行）\n", st.hasPostTags, st.existingTags)
	fmt.Printf("address_tags 列: %v → 约 %d 个地点标签\n", st.hasAddressTags, st.locRows)
	fmt.Printf("filter_results → 约 %d block + %d unmatched\n", st.blockRows, st.unmatchedRows)
	fmt.Printf("feedbacks 表: %v → %d 反馈 + %d 备注\n", st.hasFeedbacks, st.feedbackRows, st.manualRows)
}

func migrate(db *sql.DB, st planStats) error {
	if st.existingTags > 0 {
		return fmt.Errorf("post_tags 已有 %d 行，请先确认是否重复迁移", st.existingTags)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS post_tags (
		    id         INTEGER PRIMARY KEY AUTOINCREMENT,
		    post_id    INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
		    kind       TEXT    NOT NULL,
		    text       TEXT    NOT NULL,
		    source     TEXT    NOT NULL,
		    created_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_post_tags_post_id ON post_tags(post_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_tags_text ON post_tags(text)`,
		`CREATE INDEX IF NOT EXISTS idx_post_tags_kind ON post_tags(kind)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_post_tags_system_unique ON post_tags(post_id, kind, text) WHERE source = 'system'`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("建表: %w", err)
		}
	}

	now := time.Now()
	if st.hasAddressTags {
		rows, err := tx.Query(`SELECT id, address_tags FROM posts WHERE address_tags IS NOT NULL AND address_tags != '' AND address_tags != '[]'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var tagsJSON string
			if err := rows.Scan(&id, &tagsJSON); err != nil {
				rows.Close()
				return err
			}
			var locs []string
			if err := json.Unmarshal([]byte(tagsJSON), &locs); err != nil {
				rows.Close()
				return fmt.Errorf("解析 address_tags post=%d: %w", id, err)
			}
			for _, loc := range locs {
				if err := insertTag(tx, id, models.TagKindLocation, loc, models.TagSourceSystem, now); err != nil {
					rows.Close()
					return err
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
	}

	rows, err := tx.Query(`SELECT p.id, p.status, fr.status, fr.hard_rules
	    FROM posts p LEFT JOIN filter_results fr ON fr.post_id = p.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var postID int64
		var postStatus string
		var frStatus, hardJSON sql.NullString
		if err := rows.Scan(&postID, &postStatus, &frStatus, &hardJSON); err != nil {
			rows.Close()
			return err
		}
		if !frStatus.Valid {
			if postStatus == models.PostStatusRejected {
				if err := insertTag(tx, postID, models.TagKindUnmatched, models.RejectedByUnmatched, models.TagSourceSystem, now); err != nil {
					rows.Close()
					return err
				}
			}
			continue
		}
		var hard []models.RuleHit
		if hardJSON.Valid && hardJSON.String != "" {
			if err := json.Unmarshal([]byte(hardJSON.String), &hard); err != nil {
				rows.Close()
				return fmt.Errorf("解析 hard_rules post=%d: %w", postID, err)
			}
		}
		if frStatus.String == models.PostStatusRejected {
			if len(hard) > 0 {
				for _, h := range hard {
					if err := insertTag(tx, postID, models.TagKindBlock, h.Reason, models.TagSourceSystem, now); err != nil {
						rows.Close()
						return err
					}
				}
			} else if postStatus == models.PostStatusRejected {
				if err := insertTag(tx, postID, models.TagKindUnmatched, models.RejectedByUnmatched, models.TagSourceSystem, now); err != nil {
					rows.Close()
					return err
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	if st.hasFeedbacks {
		frows, err := tx.Query(`SELECT post_id, action, reason, created_at FROM feedbacks ORDER BY id`)
		if err != nil {
			return err
		}
		for frows.Next() {
			var postID int64
			var action, reason string
			var created time.Time
			if err := frows.Scan(&postID, &action, &reason, &created); err != nil {
				frows.Close()
				return err
			}
			if _, err := tx.Exec(`INSERT INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, ?)`,
				postID, models.TagKindFeedback, models.FeedbackTagText(action), models.TagSourceUser, created); err != nil {
				frows.Close()
				return err
			}
			reason = strings.TrimSpace(reason)
			if reason != "" {
				if _, err := tx.Exec(`INSERT INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, ?)`,
					postID, models.TagKindManual, reason, models.TagSourceUser, created); err != nil {
					frows.Close()
					return err
				}
			}
		}
		if err := frows.Err(); err != nil {
			return err
		}
		frows.Close()
	}

	if st.hasAddressTags {
		if _, err := tx.Exec(`ALTER TABLE posts DROP COLUMN address_tags`); err != nil {
			return fmt.Errorf("删 address_tags: %w", err)
		}
	}
	if st.hasFeedbacks {
		if _, err := tx.Exec(`DROP TABLE feedbacks`); err != nil {
			return fmt.Errorf("删 feedbacks: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM post_tags`).Scan(&n)
	fmt.Printf("post_tags 共 %d 行\n", n)
	return nil
}

func insertTag(tx *sql.Tx, postID int64, kind, text, source string, at time.Time) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO post_tags (post_id, kind, text, source, created_at) VALUES (?, ?, ?, ?, ?)`,
		postID, kind, text, source, at)
	return err
}

func backupDB(src, dst string) error {
	srcDB, err := sql.Open("sqlite", src)
	if err != nil {
		return err
	}
	defer srcDB.Close()
	_, _ = srcDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	dest := dst + ".tmp"
	if _, err := srcDB.Exec(`VACUUM INTO '` + strings.ReplaceAll(dest, "'", "''") + `'`); err != nil {
		// 老版本 sqlite 可能没有 VACUUM INTO，退回复制文件
		return copyFile(src, dst)
	}
	return os.Rename(dest, dst)
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

func tableExists(db *sql.DB, name string) bool {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
