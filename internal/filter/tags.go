package filter

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
	if len(res.HardRules) > 0 {
		for _, h := range res.HardRules {
			if !models.IsChipText(h.Reason) {
				continue
			}
			tags = append(tags, models.PostTag{
				Kind:   models.TagKindBlock,
				Text:   h.Reason,
				Source: models.TagSourceSystem,
			})
		}
		return tags
	}
	if len(locations) == 0 {
		tags = append(tags, models.PostTag{
			Kind:   models.TagKindUnmatched,
			Text:   models.RejectedByUnmatched,
			Source: models.TagSourceSystem,
		})
	}
	return tags
}
