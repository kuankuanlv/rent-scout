package filter

import (
	"context"

	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Consumer 筛选/AI 审核共用存储与规则链。
// 筛选：channel 有帖立刻硬规则判定，通过/拒绝/pending 当场落库，不调 LLM。
// AI 审核：从库读 pending 凑批，满批或 linger 才 EvaluateAIBatch。
// AI 不可用/未定案 → 默认放行（宽松模式，宁可多通知不少通知）
type Consumer struct {
	chain       *RuleChain
	store       *store.Store
	notify      chan<- struct{} // passed 帖子信号（notifier 消费）
	ai          chan<- struct{} // pending 帖子信号（ai_review 消费）
	trimLen     int             // 保留参数（旧签名兼容）；截断已下沉到 AIBatchEvaluator 按源处理
	aiBatchSize int             // AI 一次判定上限；pipeline 凑批也用这个数
}

// ConsumerOptions 消费器选项（选项模式；零值 = 与旧版 NewConsumer 行为一致）
type ConsumerOptions struct {
	// AIBatchSize AI 凑批/子批上限（规格 7.2 ai_batch_size）。≤0 = 不拆分
	AIBatchSize int
	// AITrigger 硬规则未定案写成 pending 后通知 AI 审核协程；nil = 不发
	AITrigger chan<- struct{}
}

// NewConsumer 创建 filter 消费器（旧签名兼容：无 AI 触发通道）
func NewConsumer(chain *RuleChain, st *store.Store, notify chan<- struct{}, trimLen int) *Consumer {
	return NewConsumerWithOptions(chain, st, notify, trimLen, ConsumerOptions{})
}

// NewConsumerWithOptions 创建消费器（main 传入 ai_batch_size、AI 触发通道）
func NewConsumerWithOptions(chain *RuleChain, st *store.Store, notify chan<- struct{}, trimLen int, opts ConsumerOptions) *Consumer {
	return &Consumer{chain: chain, store: st, notify: notify, ai: opts.AITrigger, trimLen: trimLen, aiBatchSize: opts.AIBatchSize}
}

// FetchCollected 筛选拉帖：只取刚入库、还没走硬规则的
func (c *Consumer) FetchCollected(ctx context.Context, limit int) ([]models.RentPost, error) {
	return c.store.FetchPendingByStatus(models.PostStatusCollected, limit)
}

// FetchPending AI 审核拉帖：只取硬规则未定案、等凑批的
func (c *Consumer) FetchPending(ctx context.Context, limit int) ([]models.RentPost, error) {
	return c.store.FetchPendingByStatus(models.PostStatusPending, limit)
}

// ProcessHard 导出包装（pipeline 筛选回调用）
func (c *Consumer) ProcessHard(ctx context.Context, batch []models.RentPost) error {
	return c.processHard(ctx, batch)
}

// ProcessAI 导出包装（pipeline AI 审核回调用）
func (c *Consumer) ProcessAI(ctx context.Context, batch []models.RentPost) error {
	return c.processAI(ctx, batch)
}

// processHard 逐帖硬规则：定案立刻写库；未定案且 AI 可用 → pending 并通知 AI；否则默认通过。
func (c *Consumer) processHard(ctx context.Context, batch []models.RentPost) error {
	if len(batch) == 0 {
		return nil
	}
	log := pkglog.Component(pkglog.Filter)
	rules, err := c.rules()
	if err != nil {
		// 规则读取失败（DB 层故障）：保持 collected 下轮重试——不流转、不写 filter_results、不发 notify。
		// 规格 5.6 仅授权"AI 链不可用/无启用规则"时默认放行；DB 故障不得静默放行（审查 K1）
		log.Error("规则读取失败", "count", len(batch), "err", err)
		return nil
	}
	hasAI := c.chain.HasAI() && len(enabledAIRules(rules)) > 0

	for i := range batch {
		post := batch[i]
		res, tags, decided, err := c.chain.EvaluateHard(ctx, post, rules)
		if err != nil {
			log.Error("硬链评估失败", "post_id", post.ID, "err", err)
			continue
		}
		if !decided {
			if hasAI {
				if err := c.store.MarkStatus([]int64{post.ID}, models.PostStatusPending); err != nil {
					return err
				}
				log.Info("未定案，交 AI 审核", "post_id", post.ID)
				c.signal(c.ai)
				continue
			}
			log.Info("AI 关闭，未定案帖默认通过", "post_id", post.ID)
			if err := c.commitStatus(post.ID, models.PostStatusPassed); err != nil {
				return err
			}
			continue
		}
		if len(tags) > 0 {
			if err := c.store.UpdatePostAddressTags(post.ID, tags); err != nil {
				log.Error("地点标签写回失败", "post_id", post.ID, "err", err)
			}
		}
		if err := c.commitDecision(res); err != nil {
			return err
		}
	}
	return nil
}

// processAI 从库汇总的 pending 凑批后调 LLM；失败保持 pending 下轮重试。
func (c *Consumer) processAI(ctx context.Context, batch []models.RentPost) error {
	if len(batch) == 0 {
		return nil
	}
	log := pkglog.Component(pkglog.AIReview)
	rules, err := c.rules()
	if err != nil {
		log.Error("规则读取失败", "count", len(batch), "err", err)
		return nil
	}
	if !c.chain.HasAI() || len(enabledAIRules(rules)) == 0 {
		log.Info("AI 关闭，未审核帖默认通过", "count", len(batch))
		ids := make([]int64, len(batch))
		for i, p := range batch {
			ids[i] = p.ID
		}
		if err := c.store.MarkStatus(ids, models.PostStatusPassed); err != nil {
			return err
		}
		c.signal(c.notify)
		return nil
	}

	for _, sub := range splitBatches(batch, c.aiBatchSize) {
		results, err := c.chain.EvaluateAIBatch(ctx, sub, rules)
		if err != nil {
			log.Warn("AI 批处理失败", "count", len(sub), "err", err)
			continue
		}
		for _, post := range sub {
			if err := c.commitDecision(results[post.ID]); err != nil {
				return err
			}
		}
		log.Info("AI 批处理完成", "count", len(sub))
	}
	return nil
}

// splitBatches 按 size 切分子批；size<=0 或不足一批时整批一次
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

// commitDecision 单帖定案立刻写结果+主状态；passed 通知 notifier
func (c *Consumer) commitDecision(res models.FilterResult) error {
	duty := pkglog.Filter
	if res.Stage == models.StageAIRule {
		duty = pkglog.AIReview
	}
	if len(res.HardRules) > 0 || res.AI != nil || res.Status == models.PostStatusRejected {
		if err := c.store.SaveFilterResult(res); err != nil {
			pkglog.Component(duty).Error("筛选结果写库失败", "post_id", res.PostID, "err", err)
		}
	}
	if err := c.commitStatus(res.PostID, res.Status); err != nil {
		return err
	}
	pkglog.Component(duty).Info("帖子已定案", "post_id", res.PostID, "stage", res.Stage, "result", res.Status,
		"reason", res.RejectedBy, "ai_reason", aiReason(res.AI))
	return nil
}

func (c *Consumer) commitStatus(postID int64, status string) error {
	if err := c.store.MarkStatus([]int64{postID}, status); err != nil {
		return err
	}
	if status == models.PostStatusPassed {
		c.signal(c.notify)
	}
	return nil
}

func (c *Consumer) signal(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func aiReason(ai *models.AIResult) string {
	if ai == nil {
		return ""
	}
	return ai.Reason
}

// rules 拉取启用规则（每次处理读取——规则可在 /admin 热变更，规格 3.3）。
// 返回 error = 规则读取失败（DB 层故障），调用方须保持待判定，不得默认放行
func (c *Consumer) rules() ([]models.Rule, error) {
	rules, err := c.store.ListRules(true)
	if err != nil {
		return nil, err
	}
	return rules, nil
}
