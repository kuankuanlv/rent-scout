package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 CGO，利于 Docker 交叉编译
)

// Store 数据访问层：单一 *sql.DB 封装
type Store struct {
	db *sql.DB
}

// Open 打开（不存在则创建）SQLite 库并执行迁移。
// 建表幂等，重复启动安全；WAL 模式提升并发读写。
func Open(dbPath string) (*Store, error) {
	// 确保目录存在（db/ 由 .gitignore 排除，运行时创建）
	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// 并发与持久性：WAL + busy_timeout（个人场景足够）
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 PRAGMA: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接
func (s *Store) Close() error { return s.db.Close() }

// migrate 建表（规格 3.5 表清单 + 状态机 2.4）
// modernc 驱动单次 Exec 只支持一条语句：逐条执行，建表幂等
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS posts (
		    id           INTEGER PRIMARY KEY AUTOINCREMENT,
		    source       TEXT    NOT NULL,
		    external_id  TEXT    NOT NULL,
		    url          TEXT    NOT NULL DEFAULT '',
		    title        TEXT    NOT NULL DEFAULT '',
		    content      TEXT    NOT NULL DEFAULT '',
		    author       TEXT    NOT NULL DEFAULT '',
		    author_url   TEXT    NOT NULL DEFAULT '',
		    published_at DATETIME,
		    collected_at DATETIME NOT NULL,
		    status       TEXT    NOT NULL DEFAULT 'collected',
		    raw          TEXT    NOT NULL DEFAULT '',
		    UNIQUE(source, external_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_posts_status ON posts(status)`,
		`CREATE TABLE IF NOT EXISTS filter_results (
		    post_id     INTEGER PRIMARY KEY,
		    status      TEXT    NOT NULL,
		    stage       TEXT    NOT NULL DEFAULT '',
		    rejected_by TEXT    NOT NULL DEFAULT '',
		    decided_at  DATETIME NOT NULL,
		    hard_rules  TEXT    NOT NULL DEFAULT '[]',
		    ai_result   TEXT    NOT NULL DEFAULT '',
		    FOREIGN KEY(post_id) REFERENCES posts(id)
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
		    id         INTEGER PRIMARY KEY AUTOINCREMENT,
		    name       TEXT    NOT NULL,
		    type       TEXT    NOT NULL,
		    mode       TEXT    NOT NULL,
		    value      TEXT    NOT NULL,
		    enabled    INTEGER NOT NULL DEFAULT 1,
		    priority   INTEGER NOT NULL DEFAULT 0,
		    created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
		    id         INTEGER PRIMARY KEY AUTOINCREMENT,
		    post_id    INTEGER NOT NULL,
		    channel    TEXT    NOT NULL,
		    status     TEXT    NOT NULL DEFAULT 'pending',
		    attempts   INTEGER NOT NULL DEFAULT 0,
		    last_error TEXT    NOT NULL DEFAULT '',
		    sent_at    DATETIME,
		    UNIQUE(post_id, channel),
		    FOREIGN KEY(post_id) REFERENCES posts(id)
		)`,
		`CREATE TABLE IF NOT EXISTS feedbacks (
		    id         INTEGER PRIMARY KEY AUTOINCREMENT,
		    post_id    INTEGER NOT NULL,
		    channel    TEXT    NOT NULL DEFAULT '',
		    action     TEXT    NOT NULL,
		    reason     TEXT    NOT NULL DEFAULT '',
		    created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS source_state (
		    source     TEXT PRIMARY KEY,
		    cursor     TEXT NOT NULL DEFAULT '',
		    updated_at DATETIME NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("迁移建表: %w", err)
		}
	}
	return nil
}
