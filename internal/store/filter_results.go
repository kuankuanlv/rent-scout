package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// ListFilterTags 帖子页标签下拉：已写过的 address_tags ∪ 启用白名单词，去重排序
func (s *Store) ListFilterTags() ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(raw string) {
		t := strings.TrimSpace(raw)
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	rows, err := s.db.Query(`SELECT DISTINCT j.value FROM posts, json_each(posts.address_tags) AS j
		WHERE typeof(j.value) = 'text' AND TRIM(j.value) != ''`)
	if err != nil {
		return nil, fmt.Errorf("列举地址标签: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		add(v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rules, err := s.ListRules(true)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.Type != models.RuleTypeWhitelist {
			continue
		}
		for _, kw := range strings.FieldsFunc(r.Value, isTagSep) {
			add(kw)
		}
	}
	sort.Strings(out)
	return out, nil
}

func isTagSep(r rune) bool {
	return r == ',' || r == '\n' || r == '，' || r == '、'
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

// AttachHitTags 给列表帖补全展示用命中标签：白名单走 address_tags，黑名单/AI 走 filter_results
func (s *Store) AttachHitTags(list []models.RentPost) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]any, len(list))
	placeholders := make([]string, len(list))
	for i, p := range list {
		ids[i] = p.ID
		placeholders[i] = "?"
	}
	q := `SELECT post_id, status, stage, rejected_by, hard_rules, ai_result FROM filter_results WHERE post_id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.Query(q, ids...)
	if err != nil {
		return fmt.Errorf("查命中标签: %w", err)
	}
	defer rows.Close()
	byID := map[int64]models.FilterResult{}
	for rows.Next() {
		var fr models.FilterResult
		var hardJSON, aiJSON string
		if err := rows.Scan(&fr.PostID, &fr.Status, &fr.Stage, &fr.RejectedBy, &hardJSON, &aiJSON); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(hardJSON), &fr.HardRules); err != nil {
			return fmt.Errorf("解析命中规则: %w", err)
		}
		if aiJSON != "" {
			fr.AI = &models.AIResult{}
			if err := json.Unmarshal([]byte(aiJSON), fr.AI); err != nil {
				return fmt.Errorf("解析 AI 结果: %w", err)
			}
		}
		byID[fr.PostID] = fr
	}
	if err := rows.Err(); err != nil {
		return err
	}
		for i := range list {
			fr, ok := byID[list[i].ID]
			list[i].HitTags = hitTagsFrom(&list[i], fr, ok)
			if ok && fr.AI != nil {
				list[i].AIReason = fr.AI.Reason
			}
		}
	return nil
}

func hitTagsFrom(p *models.RentPost, fr models.FilterResult, hasFR bool) []models.HitTag {
	var out []models.HitTag
	seen := map[string]bool{}
	add := func(kind, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := kind + "|" + text
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, models.HitTag{Kind: kind, Text: text})
	}
	for _, t := range p.AddressTags {
		add("whitelist", t)
	}
		if !hasFR {
			if p.Status == models.PostStatusRejected {
				add("blacklist", "默认拒绝")
			}
			return out
		}
		hardKind := "whitelist"
		if p.Status == models.PostStatusRejected || fr.Status == models.PostStatusRejected {
			hardKind = "blacklist"
		}
		for _, h := range fr.HardRules {
			for _, part := range strings.FieldsFunc(h.Reason, isTagSep) {
				add(hardKind, part)
			}
		}
		if (p.Status == models.PostStatusRejected || fr.Status == models.PostStatusRejected) &&
			len(fr.HardRules) == 0 && fr.AI == nil {
			add("blacklist", "默认拒绝")
		}
	if fr.AI != nil {
		text := "AI"
		if strings.TrimSpace(fr.AI.Reason) != "" {
			text = truncateRunes(strings.TrimSpace(fr.AI.Reason), 20)
		}
		add("ai", text)
	}
	return out
}

func truncateRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
