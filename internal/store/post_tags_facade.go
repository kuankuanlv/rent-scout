package store

import (
	"encoding/json"
	"fmt"
	"strings"

	"rent-scout/internal/models"
	"rent-scout/internal/store/post_tags"
)

// AttachPostTags 列表帖补全 Tags
func (s *Store) AttachPostTags(list []models.RentPost) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, len(list))
	for i, p := range list {
		ids[i] = p.ID
	}
	byID, err := s.postTags.ListByPostIDs(ids)
	if err != nil {
		return err
	}
	for i := range list {
		tags := byID[list[i].ID]
		posttags.SortTags(tags)
		list[i].Tags = tags
	}
	return nil
}

// AttachAIReasons 列表帖补全 AIReason（filter_results）
func (s *Store) AttachAIReasons(list []models.RentPost) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]any, len(list))
	placeholders := make([]string, len(list))
	for i, p := range list {
		ids[i] = p.ID
		placeholders[i] = "?"
	}
	q := `SELECT post_id, ai_result FROM filter_results WHERE post_id IN (` + joinPlaceholders(len(list)) + `) AND ai_result != ''`
	rows, err := s.db.Query(q, ids...)
	if err != nil {
		return fmt.Errorf("查 AI 原因: %w", err)
	}
	defer rows.Close()
	byID := map[int64]string{}
	for rows.Next() {
		var postID int64
		var aiJSON string
		if err := rows.Scan(&postID, &aiJSON); err != nil {
			return err
		}
		ai := &models.AIResult{}
		if err := json.Unmarshal([]byte(aiJSON), ai); err != nil {
			return fmt.Errorf("解析 AI 结果: %w", err)
		}
		byID[postID] = ai.Reason
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range list {
		list[i].AIReason = byID[list[i].ID]
	}
	return nil
}

func joinPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}

// ListFilterTags 标签下拉
func (s *Store) ListFilterTags() ([]string, error) {
	return s.postTags.ListDistinctTexts()
}

// ReplaceSystemTags 硬规则定案写系统标签
func (s *Store) ReplaceSystemTags(postID int64, tags []models.PostTag) error {
	return s.postTags.ReplaceSystemTags(postID, tags)
}

// AddUserFeedback 人工有用/无用 + 可选备注
func (s *Store) AddUserFeedback(postID int64, action, reason string) error {
	tags := []models.PostTag{{
		Kind:   models.TagKindFeedback,
		Text:   models.FeedbackTagText(action),
		Source: models.TagSourceUser,
	}}
	reason = strings.TrimSpace(reason)
	if reason != "" {
		tags = append(tags, models.PostTag{
			Kind:   models.TagKindManual,
			Text:   reason,
			Source: models.TagSourceUser,
		})
	}
	return s.postTags.AddUserTags(postID, tags)
}

// ListTagsByPost 单帖标签
func (s *Store) ListTagsByPost(postID int64) ([]models.PostTag, error) {
	return s.postTags.ListByPostID(postID)
}
