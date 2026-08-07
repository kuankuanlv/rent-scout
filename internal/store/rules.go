package store

import (
	"fmt"
	"time"

	"rent-scout/internal/models"
)

// CreateRule 新建规则，返回带 ID 的完整规则
func (s *Store) CreateRule(r models.Rule) (models.Rule, error) {
	res, err := s.db.Exec(`INSERT INTO rules (name, type, mode, value, enabled, priority, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Name, r.Type, r.Mode, r.Value, r.Enabled, r.Priority, time.Now())
	if err != nil {
		return r, fmt.Errorf("新建规则: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return r, err
	}
	r.ID = id
	r.CreatedAt = time.Now()
	return r, nil
}

// ListRules 规则列表；onlyEnabled=true 只取启用，按 priority 降序（先执行优先级高的）
func (s *Store) ListRules(onlyEnabled bool) ([]models.Rule, error) {
	query := `SELECT id, name, type, mode, value, enabled, priority, created_at FROM rules`
	if onlyEnabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY priority DESC, id ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("查规则: %w", err)
	}
	defer rows.Close()
	var rules []models.Rule
	for rows.Next() {
		var r models.Rule
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Mode, &r.Value, &r.Enabled, &r.Priority, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateRule 全量更新（/admin 规则管理用）
func (s *Store) UpdateRule(r models.Rule) error {
	_, err := s.db.Exec(`UPDATE rules SET name=?, type=?, mode=?, value=?, enabled=?, priority=? WHERE id=?`,
		r.Name, r.Type, r.Mode, r.Value, r.Enabled, r.Priority, r.ID)
	if err != nil {
		return fmt.Errorf("更新规则: %w", err)
	}
	return nil
}

// DeleteRule 删除规则
func (s *Store) DeleteRule(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM rules WHERE id=?`, id); err != nil {
		return fmt.Errorf("删除规则: %w", err)
	}
	return nil
}
