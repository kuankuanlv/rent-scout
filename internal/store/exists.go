package store

import (
	"fmt"
	"strings"
)

// ExistsByExternalIDs 批量查重（调整规格 E）：返回 ids 中已存在的集合。
// 列表页解析后先行调用，已存在的帖子不抓详情页（省请求）
func (s *Store) ExistsByExternalIDs(source string, ids []string) (map[string]bool, error) {
	existing := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return existing, nil
	}
	// 参数化 IN 查询；ids 由源解析产生（非用户输入），仍走占位符防注入
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, source)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.Query(`SELECT external_id FROM posts WHERE source = ? AND external_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("批量查重: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}
