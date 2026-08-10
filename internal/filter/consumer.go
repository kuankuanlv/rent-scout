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
	chain       *RuleChain
	store       *store.Store
	notify      chan<- struct{} // passed 帖子信号（计划 4 notifier 消费）
	trimLen     int             // 保留参数（旧签名兼容）；截断已下沉到 AIBatchEvaluator 按源处理
	aiBatchSize int             // AI 子批上限（ai_batch_size）：未定案帖数超过时拆分多次 EvaluateAIBatch
}

// ConsumerOptions 消费器选项（选项模式；零值 = 与旧版 NewConsumer 行为一致）
type ConsumerOptions struct {
	// AIBatchSize AI 子批上限：batch 内未定案帖数 > 该值时拆分为多次 EvaluateAIBatch
	// 调用（每次 ≤ 该值，规格 7.2 ai_batch_size）。≤0 = 不拆分（整批一次调用，向后兼容）
	AIBatchSize int
}

// NewConsumer 创建 filter 消费器（旧签名兼容：AI 未定案整批一次调用）
func NewConsumer(chain *RuleChain, st *store.Store, notify chan<- struct{}, trimLen int) *Consumer {
	return NewConsumerWithOptions(chain, st, notify, trimLen, ConsumerOptions{})
}

// NewConsumerWithOptions 创建 filter 消费器（main 传入 ai_batch_size 等选项）
func NewConsumerWithOptions(chain *RuleChain, st *store.Store, notify chan<- struct{}, trimLen int, opts ConsumerOptions) *Consumer {
	return &Consumer{chain: chain, store: st, notify: notify, trimLen: trimLen, aiBatchSize: opts.AIBatchSize}
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
	rules, err := c.rules()
	if err != nil {
		// 规则读取失败（DB 层故障）：整批保持待判定下轮重试——不流转、不写 filter_results、不发 notify。
		// 规格 5.6 仅授权"AI 链不可用/无启用规则"时默认放行；DB 故障不得静默放行（审查 K1）
		slog.Error("规则读取失败，整批保持待判定（不放行）", "count", len(batch), "err", err)
		return nil
	}
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

	// ② AI 批：未定案帖子按 ai_batch_size 拆子批调用 LLM（规格 5.4 批量语义 + 7.2 ai_batch_size）。
	// 子批相互独立：某子批瞬时失败仅该子批保持 pending 下轮重试，不误标记、不影响其余子批（规格 5.6）
	if len(aiPending) > 0 {
		if c.chain.HasAI() && len(enabledAIRules(rules)) > 0 {
			for _, sub := range splitBatches(aiPending, c.aiBatchSize) {
				results, err := c.chain.EvaluateAIBatch(ctx, sub, rules)
				if err != nil {
					// 瞬时失败（429/5xx/解析）：该子批保持 pending 下轮重试，不误标记（规格 5.6）
					slog.Warn("AI 批量判定失败，保持待判定", "count", len(sub), "err", err)
					for _, post := range sub {
						pendingIDs = append(pendingIDs, post.ID)
					}
					continue
				}
				for _, post := range sub {
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

	// ③ 批量状态流转：passed/rejected/pending 三次独立 UPDATE（非原子，状态机见规格 2.4）。
	// SQLite 单写者 + 批处理单 goroutine 下可接受；若需整批原子需改事务变体（不建议，失败态可重试收敛）
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

// splitBatches 按 size 切分子批；size<=0 或不足一批时整批一次（向后兼容，不改变旧行为）
func splitBatches(posts []models.RentPost, size int) [][]models.RentPost {
	if size <= 0 || len(posts) <= size {
		return [][]models.RentPost{posts}
	}
	out := make([][]models.RentPost, 0, (len(posts)+size-1)/size)
	for i := 0; i < len(posts); i += size {
		end := i + size
		if end > len(posts) {
			end = len(posts)
		}
		out = append(out, posts[i:end])
	}
	return out
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

// rules 拉取启用规则（每次批处理读取——规则可在 /admin 热变更，规格 3.3）。
// 返回 error = 规则读取失败（DB 层故障），调用方须整批保持待判定，不得默认放行
func (c *Consumer) rules() ([]models.Rule, error) {
	rules, err := c.store.ListRules(true)
	if err != nil {
		return nil, err
	}
	return rules, nil
}
