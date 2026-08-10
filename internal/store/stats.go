package store

import (
	"database/sql"
	"fmt"
)

// RuleStat 单条规则命中统计（/admin 报表用，规格 5.5）
type RuleStat struct {
	RuleID       int64
	Hits         int // passed 帖子中该规则被命中的次数
	UselessCount int // 命中帖中被用户标 useless 的帖子数（反馈负向归因）
}

// RuleHitStats 规则命中 × 反馈负向统计。
// hard_rules 是 JSON 数组（json_each 展开）；负向归因：命中规则的帖子被标 useless 即计 1
// （v1 近似：归因给帖子的全部命中规则）
func (s *Store) RuleHitStats() ([]RuleStat, error) {
	rows, err := s.db.Query(`SELECT CAST(hr.value->>'ruleId' AS INTEGER) AS rule_id, COUNT(*),
	        COUNT(DISTINCT CASE WHEN f.id IS NOT NULL THEN fr.post_id END)
	    FROM filter_results fr
	    JOIN json_each(fr.hard_rules) AS hr
	    LEFT JOIN feedbacks f ON f.post_id = fr.post_id AND f.action = 'useless'
	    WHERE fr.status = 'passed' AND hr.value IS NOT NULL
	    GROUP BY rule_id
	    ORDER BY rule_id`)
	if err != nil {
		return nil, fmt.Errorf("规则命中统计: %w", err)
	}
	defer rows.Close()
	var stats []RuleStat
	for rows.Next() {
		var st RuleStat
		var useless sql.NullInt64
		if err := rows.Scan(&st.RuleID, &st.Hits, &useless); err != nil {
			return nil, err
		}
		if useless.Valid {
			st.UselessCount = int(useless.Int64)
		}
		stats = append(stats, st)
	}
	return stats, rows.Err()
}
