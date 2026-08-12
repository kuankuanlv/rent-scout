package store

import "fmt"

// TodayStats 今日概览（规格 7.4 /admin 报表）：posts 今日采集数 + filter_results 今日判定分布
type TodayStats struct {
	Collected int // posts collected_at 今日
	Passed    int // filter_results status=passed 且 decided_at 今日
	Rejected  int // filter_results status=rejected 且 decided_at 今日
	Pending   int // filter_results status=pending 且 decided_at 今日
}

// TodayStats 今日概览（按本机本地日期聚合，date('now','localtime')）。
// 注意：modernc 驱动把 time.Time 以 Go String() 格式落库（"YYYY-MM-DD HH:MM:SS ... CST"），
// SQLite 的 date() 无法直接解析该格式（返回 NULL），故取定宽日期前缀 substr(x,1,10) 比较
func (s *Store) TodayStats() (TodayStats, error) {
	var st TodayStats
	err := s.db.QueryRow(`SELECT
	    (SELECT COUNT(*) FROM posts WHERE substr(collected_at,1,10) = date('now','localtime')),
	    (SELECT COUNT(*) FROM filter_results WHERE status='passed' AND substr(decided_at,1,10) = date('now','localtime')),
	    (SELECT COUNT(*) FROM filter_results WHERE status='rejected' AND substr(decided_at,1,10) = date('now','localtime')),
	    (SELECT COUNT(*) FROM filter_results WHERE status='pending' AND substr(decided_at,1,10) = date('now','localtime'))`).
		Scan(&st.Collected, &st.Passed, &st.Rejected, &st.Pending)
	if err != nil {
		return st, fmt.Errorf("今日统计: %w", err)
	}
	return st, nil
}

// ChannelStat 单渠道发送统计（规格 7.4 各渠道发送成功率）
type ChannelStat struct {
	Channel string
	Sent    int
	Failed  int
	Dead    int
}

// ChannelStats 渠道发送统计（notifications 全量，历史累计）。
// 布尔求和：SQLite 中 status='sent' 等表达式求值为 0/1
func (s *Store) ChannelStats() ([]ChannelStat, error) {
	rows, err := s.db.Query(`SELECT channel,
	    SUM(status='sent'), SUM(status='failed'), SUM(status='dead')
	    FROM notifications GROUP BY channel ORDER BY channel`)
	if err != nil {
		return nil, fmt.Errorf("渠道发送统计: %w", err)
	}
	defer rows.Close()
	var stats []ChannelStat
	for rows.Next() {
		var st ChannelStat
		if err := rows.Scan(&st.Channel, &st.Sent, &st.Failed, &st.Dead); err != nil {
			return nil, err
		}
		stats = append(stats, st)
	}
	return stats, rows.Err()
}
