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
		`CREATE TABLE IF NOT EXISTS kv_config (
		    key        TEXT PRIMARY KEY,
		    value      TEXT NOT NULL,
		    updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS config_history (
		    id         INTEGER PRIMARY KEY AUTOINCREMENT,
		    key        TEXT NOT NULL,
		    old_value  TEXT NOT NULL DEFAULT '',
		    new_value  TEXT NOT NULL DEFAULT '',
		    created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_config_history_key ON config_history(key)`,
		`CREATE INDEX IF NOT EXISTS idx_config_history_created ON config_history(created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("迁移建表: %w", err)
		}
	}
	// 追加列（调整规格 2.3）：CREATE IF NOT EXISTS 对已有库不加列，须显式 ALTER；
	// 检查 PRAGMA table_info 存在性，幂等安全
	colExists, err := s.columnExists("posts", "address_tags")
	if err != nil {
		return err
	}
	if !colExists {
		if _, err := s.db.Exec(`ALTER TABLE posts ADD COLUMN address_tags TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("追加 address_tags 列: %w", err)
		}
	}
	// 已处理时间列：NULL=未处理；幂等 ALTER（仿 address_tags）
	handledExists, err := s.columnExists("posts", "handled_at")
	if err != nil {
		return err
	}
	if !handledExists {
		if _, err := s.db.Exec(`ALTER TABLE posts ADD COLUMN handled_at DATETIME NULL`); err != nil {
			return fmt.Errorf("追加 handled_at 列: %w", err)
		}
	}
	// 规则 type：旧 hard_* / hard_keyword+mode → whitelist|blacklist|ai_natural（Spec 09 §2.2）
	if err := s.migrateRuleTypes(); err != nil {
		return err
	}
	return nil
}

// migrateRuleTypes 把旧四 type+mode 写成三 type；幂等可重复跑
func (s *Store) migrateRuleTypes() error {
	stmts := []struct {
		sql  string
		what string
	}{
		{`UPDATE rules SET type='whitelist' WHERE type='hard_whitelist'`, "hard_whitelist→whitelist"},
		{`UPDATE rules SET type='blacklist' WHERE type='hard_blacklist'`, "hard_blacklist→blacklist"},
		{`UPDATE rules SET type='whitelist' WHERE type='hard_keyword' AND mode='include'`, "hard_keyword+include→whitelist"},
		{`UPDATE rules SET type='blacklist' WHERE type='hard_keyword'`, "hard_keyword→blacklist"},
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st.sql); err != nil {
			return fmt.Errorf("迁移规则类型(%s): %w", st.what, err)
		}
	}
	return nil
}

// columnExists 检查表是否已存在指定列（迁移幂等辅助）
func (s *Store) columnExists(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
