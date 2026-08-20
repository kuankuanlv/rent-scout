package tag

import "rent-scout/internal/models"

// SystemTagsFromHard 硬规则定案时写入 post_tags 的 system 行
func SystemTagsFromHard(res models.FilterResult, locations []string) []models.PostTag {
	var tags []models.PostTag
	for _, loc := range locations {
		if !models.IsChipText(loc) {
			continue
		}
		tags = append(tags, models.PostTag{
			Kind:   models.TagKindLocation,
			Text:   loc,
			Source: models.TagSourceSystem,
		})
	}
	if res.Status != models.PostStatusRejected {
		return tags
	}
	// 拒绝不再把黑名单词（中介/代理）写成标签：正文里「非中介」也会子串命中，标未命中就行
	if len(locations) == 0 {
		tags = append(tags, models.PostTag{
			Kind:   models.TagKindUnmatched,
			Text:   models.RejectedByUnmatched,
			Source: models.TagSourceSystem,
		})
	}
	return tags
}
