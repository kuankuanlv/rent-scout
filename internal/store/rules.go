package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"rent-scout/internal/models"
)

// ErrLastEnabledRule 删除或禁用会导致启用规则总数变为 0
var ErrLastEnabledRule = errors.New("至少保留一条启用规则")

// DefaultLocationRule 启用规则数为 0 时写入的默认白名单（Spec 09 §2.2）
func DefaultLocationRule() models.Rule {
	return models.Rule{
		Name:     "默认地点",
		Type:     models.RuleTypeWhitelist,
		Value:    "雍和宫,和平里",
		Enabled:  true,
		Priority: 100,
	}
}

// CreateRule 新建规则，返回带 ID 的完整规则
func (s *Store) CreateRule(rule models.Rule) (models.Rule, error) {
	res, err := s.db.Exec(`INSERT INTO rules (name, type, mode, value, enabled, priority, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rule.Name, rule.Type, rule.Mode, rule.Value, rule.Enabled, rule.Priority, time.Now())
	if err != nil {
		return rule, fmt.Errorf("新建规则: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return rule, err
	}
	rule.ID = id
	rule.CreatedAt = time.Now()
	return rule, nil
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
	var out []models.Rule
	for rows.Next() {
		var rule models.Rule
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Type, &rule.Mode, &rule.Value, &rule.Enabled, &rule.Priority, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// CountEnabledRules 启用规则总数（任意类型合计）
func (s *Store) CountEnabledRules() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rules WHERE enabled = 1`).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计启用规则: %w", err)
	}
	return n, nil
}

// EnsureDefaultRule 启用规则数为 0 时写入默认地点白名单
func (s *Store) EnsureDefaultRule() error {
	n, err := s.CountEnabledRules()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := s.CreateRule(DefaultLocationRule()); err != nil {
		return fmt.Errorf("种子默认规则: %w", err)
	}
	return nil
}

// UpdateRule 全量更新（/admin 规则管理用）；禁止禁用导致启用总数为 0
func (s *Store) UpdateRule(rule models.Rule) error {
	if !rule.Enabled {
		var currentlyEnabled bool
		err := s.db.QueryRow(`SELECT enabled FROM rules WHERE id=?`, rule.ID).Scan(&currentlyEnabled)
		if err == sql.ErrNoRows {
			return fmt.Errorf("更新规则: 不存在 id=%d", rule.ID)
		}
		if err != nil {
			return fmt.Errorf("更新规则: %w", err)
		}
		if currentlyEnabled {
			n, err := s.CountEnabledRules()
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastEnabledRule
			}
		}
	}
	_, err := s.db.Exec(`UPDATE rules SET name=?, type=?, mode=?, value=?, enabled=?, priority=? WHERE id=?`,
		rule.Name, rule.Type, rule.Mode, rule.Value, rule.Enabled, rule.Priority, rule.ID)
	if err != nil {
		return fmt.Errorf("更新规则: %w", err)
	}
	return nil
}

// DeleteRule 删除规则；禁止删除导致启用总数为 0
func (s *Store) DeleteRule(id int64) error {
	var enabled bool
	err := s.db.QueryRow(`SELECT enabled FROM rules WHERE id=?`, id).Scan(&enabled)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("删除规则: %w", err)
	}
	if enabled {
		n, err := s.CountEnabledRules()
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastEnabledRule
		}
	}
	if _, err := s.db.Exec(`DELETE FROM rules WHERE id=?`, id); err != nil {
		return fmt.Errorf("删除规则: %w", err)
	}
	return nil
}
