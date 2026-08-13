package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"rent-scout/internal/models"
)

// SaveFilterResult 写入/覆盖筛选结果（1:1 posts，upsert）。
// hard_rules 与 ai_result 以 JSON 存储（规格 3.2）
func (s *Store) SaveFilterResult(fr models.FilterResult) error {
	// nil HardRules 归一化为空数组（与 InsertPost 的 nil→[] 一致）：JSON 存 "[]" 而非 "null"。
	// 否则 json_each('null') 会产出 value=NULL 的假行 → RuleHitStats 幽灵 rule_id=0 统计（审查 K2）
	hardRules := fr.HardRules
	if hardRules == nil {
		hardRules = []models.RuleHit{}
	}
	hardJSON, err := json.Marshal(hardRules)
	if err != nil {
		return fmt.Errorf("序列化命中规则: %w", err)
	}
	var aiJSON string
	if fr.AI != nil {
		b, err := json.Marshal(fr.AI)
		if err != nil {
			return fmt.Errorf("序列化 AI 结果: %w", err)
		}
		aiJSON = string(b)
	}
	_, err = s.db.Exec(`INSERT INTO filter_results (post_id, status, stage, rejected_by, decided_at, hard_rules, ai_result)
	    VALUES (?, ?, ?, ?, ?, ?, ?)
	    ON CONFLICT(post_id) DO UPDATE SET status=excluded.status, stage=excluded.stage,
	    rejected_by=excluded.rejected_by, decided_at=excluded.decided_at,
	    hard_rules=excluded.hard_rules, ai_result=excluded.ai_result`,
		fr.PostID, fr.Status, fr.Stage, fr.RejectedBy, fr.DecidedAt, string(hardJSON), aiJSON)
	if err != nil {
		return fmt.Errorf("保存筛选结果: %w", err)
	}
	return nil
}

// UpdatePostAddressTags 写回地址标签（白名单命中，调整规格 A 2.3）。
// EvaluateHard 只产出内存 tags，入库需显式更新 posts.address_tags
func (s *Store) UpdatePostAddressTags(postID int64, tags []string) error {
	b, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("序列化地址标签: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE posts SET address_tags=? WHERE id=?`, string(b), postID); err != nil {
		return fmt.Errorf("写回地址标签: %w", err)
	}
	return nil
}

// FilterResultByPostID 回读筛选结果（ok=false = 尚未判定）
func (s *Store) FilterResultByPostID(postID int64) (models.FilterResult, bool, error) {
	var fr models.FilterResult
	var hardJSON, aiJSON string
	err := s.db.QueryRow(`SELECT post_id, status, stage, rejected_by, decided_at, hard_rules, ai_result
	    FROM filter_results WHERE post_id=?`, postID).
		Scan(&fr.PostID, &fr.Status, &fr.Stage, &fr.RejectedBy, &fr.DecidedAt, &hardJSON, &aiJSON)
	if err == sql.ErrNoRows {
		return fr, false, nil
	}
	if err != nil {
		return fr, false, fmt.Errorf("查筛选结果: %w", err)
	}
	if err := json.Unmarshal([]byte(hardJSON), &fr.HardRules); err != nil {
		return fr, false, fmt.Errorf("解析命中规则: %w", err)
	}
	if aiJSON != "" {
		fr.AI = &models.AIResult{}
		if err := json.Unmarshal([]byte(aiJSON), fr.AI); err != nil {
			return fr, false, fmt.Errorf("解析 AI 结果: %w", err)
		}
	}
	return fr, true, nil
}
