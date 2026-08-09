package filter

import (
	"context"
	"log/slog"

	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// Consumer filter 消费器（规格 2.3）：由 pipeline.Consumer 驱动。
// 编排（规格 5.3 + 调整 C）：逐帖硬编码链（EvaluateHard）→ 未定案聚合为 AI 批一次调用
// （EvaluateAIBatch）→ 状态流转 + 写结果 + passed 触发 notify。
// AI 不可用/未定案 → 默认放行（宽松模式，宁可多通知不少通知）
type Consumer struct {
	chain   *RuleChain
	store   *store.Store
	notify  chan<- struct{} // passed 帖子信号（计划 4 notifier 消费）
	trimLen int
}

// NewConsumer 创建 filter 消费器
func NewConsumer(chain *RuleChain, st *store.Store, notify chan<- struct{}, trimLen int) *Consumer {
	return &Consumer{chain: chain, store: st, notify: notify, trimLen: trimLen}
}

// FetchBatch 拉批：collected（待首次判定）+ pending（瞬时失败待重试），按 id 升序限量
func (c *Consumer) FetchBatch(ctx context.Context, limit int) ([]models.RentPost, error) {
	return c.store.FetchPendingByStatuses([]string{models.PostStatusCollected, models.PostStatusPending}, limit)
}

// ProcessBatch 导出包装（pipeline 回调用）
func (c *Consumer) ProcessBatch(ctx context.Context, batch []models.RentPost) error {
	return c.processBatch(ctx, batch)
}

// processBatch 批处理（测试可见）：硬编码逐帖定案 → AI 批聚合 → 状态流转。
// 返回 error 仅记录；单帖失败不影响批内其余
func (c *Consumer) processBatch(ctx context.Context, batch []models.RentPost) error {
	rules := c.rules()
	var passedIDs, rejectedIDs, pendingIDs []int64
	var aiPending []models.RentPost

	// ① 硬编码链逐帖定案（白名单短路/黑名单/关键词）
	for i := range batch {
		post := batch[i]
		res, tags, decided, err := c.chain.EvaluateHard(ctx, post, rules)
		if err != nil {
			slog.Error("硬编码判定失败", "post_id", post.ID, "err", err)
			pendingIDs = append(pendingIDs, post.ID)
			continue
		}
		if !decided {
			aiPending = append(aiPending, post) // 未定案：进 AI 批
			continue
		}
		// 定案：白名单 tags 写库（调整规格 A 2.3）
		if len(tags) > 0 {
			if err := c.store.UpdatePostAddressTags(post.ID, tags); err != nil {
				slog.Error("写回地址标签失败", "post_id", post.ID, "err", err)
			}
		}
		c.recordDecision(res, &passedIDs, &rejectedIDs)
	}

	// ② AI 批：未定案帖子一次 LLM 调用（规格 5.4 批量语义）
	if len(aiPending) > 0 {
		if c.chain.HasAI() && len(enabledAIRules(rules)) > 0 {
			results, err := c.chain.EvaluateAIBatch(ctx, aiPending, rules)
			if err != nil {
				// 瞬时失败（429/5xx/解析）：整批保持 pending 下轮重试，不误标记（规格 5.6）
				slog.Warn("AI 批量判定失败，保持待判定", "count", len(aiPending), "err", err)
				for _, post := range aiPending {
					pendingIDs = append(pendingIDs, post.ID)
				}
			} else {
				for _, post := range aiPending {
					res := results[post.ID]
					slog.Info("post_decided", "post_id", post.ID, "stage", res.Stage, "result", res.Status,
						"reason", res.RejectedBy, "ai_reason", aiReason(res.AI))
					c.recordDecision(res, &passedIDs, &rejectedIDs)
				}
			}
		} else {
			// 无 AI（未配 key/已关闭）：默认放行（宽松模式）
			slog.Info("AI 未启用，未定案帖子默认放行", "count", len(aiPending))
			for _, post := range aiPending {
				passedIDs = append(passedIDs, post.ID)
			}
		}
	}

	// ③ 批量状态流转（原子，规格 2.4）
	if len(passedIDs) > 0 {
		if err := c.store.MarkStatus(passedIDs, models.PostStatusPassed); err != nil {
			return err
		}
		if c.notify != nil {
			select {
			case c.notify <- struct{}{}:
			default: // 满则丢：下游 linger 兜底（规格 2.3）
			}
		}
	}
	if len(rejectedIDs) > 0 {
		if err := c.store.MarkStatus(rejectedIDs, models.PostStatusRejected); err != nil {
			return err
		}
	}
	if len(pendingIDs) > 0 {
		if err := c.store.MarkStatus(pendingIDs, models.PostStatusPending); err != nil {
			return err
		}
	}
	return nil
}

// recordDecision 记录单帖判定：写筛选结果（有内容时）+ 收集状态 ID + 日志（规格 8.3）
func (c *Consumer) recordDecision(res models.FilterResult, passedIDs, rejectedIDs *[]int64) {
	if len(res.HardRules) > 0 || res.AI != nil || res.Status == models.PostStatusRejected {
		if err := c.store.SaveFilterResult(res); err != nil {
			slog.Error("保存筛选结果失败", "post_id", res.PostID, "err", err)
		}
	}
	if res.Status == models.PostStatusPassed {
		*passedIDs = append(*passedIDs, res.PostID)
	} else {
		*rejectedIDs = append(*rejectedIDs, res.PostID)
	}
	// 日志规范 8.3：逐帖判定结果
	slog.Info("post_decided", "post_id", res.PostID, "stage", res.Stage, "result", res.Status,
		"reason", res.RejectedBy, "ai_reason", aiReason(res.AI))
}

// aiReason AI 推荐/拒绝理由（日志展示）
func aiReason(ai *models.AIResult) string {
	if ai == nil {
		return ""
	}
	return ai.Reason
}

// rules 拉取启用规则（每次批处理读取——规则可在 /admin 热变更，规格 3.3）
func (c *Consumer) rules() []models.Rule {
	rules, err := c.store.ListRules(true)
	if err != nil {
		slog.Error("拉取规则失败", "err", err)
		return nil
	}
	return rules
}
