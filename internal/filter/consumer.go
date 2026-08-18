package filter

import (
	"context"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Consumer 硬规则与 AI 审核共用存储与规则链，互不 signal。
type Consumer struct {
	chain       *RuleChain
	store       *store.Store
	rt          *config.HotConfig
	aiBatchSize int
}

type ConsumerOptions struct {
	AIBatchSize int
	HotConfig   *config.HotConfig
}

func NewConsumerWithOptions(chain *RuleChain, st *store.Store, opts ConsumerOptions) *Consumer {
	return &Consumer{chain: chain, store: st, rt: opts.HotConfig, aiBatchSize: opts.AIBatchSize}
}

func (c *Consumer) FetchCollected(ctx context.Context, limit int) ([]models.RentPost, error) {
	return c.store.FetchPendingByStatus(models.PostStatusCollected, limit)
}

// FetchAwaitingAI 仅 AI 协程调用：没开 AI 或没有启用的 AI 规则就空捞；否则捞已通过且还没 ai_result 的帖。
func (c *Consumer) FetchAwaitingAI(ctx context.Context, limit int) ([]models.RentPost, error) {
	log := pkglog.Component(pkglog.AIReview)
	if c.rt != nil {
		if ev, reason := LiveAIEvaluator(c.rt); ev == nil {
			log.Info(reason)
			return nil, nil
		}
	} else if c.chain == nil || !c.chain.HasAI() {
		return nil, nil
	}
	rules, err := c.rules()
	if err != nil {
		log.Error("规则读取失败", "err", err)
		return nil, nil
	}
	if len(enabledAIRules(rules)) == 0 {
		log.Info("当前配置没有启用的 AI 规则，无需执行")
		return nil, nil
	}
	return c.store.FetchPassedWithoutAI(limit)
}

func (c *Consumer) ProcessHard(ctx context.Context, batch []models.RentPost) error {
	return c.processHard(ctx, batch)
}

func (c *Consumer) ProcessAI(ctx context.Context, batch []models.RentPost) error {
	return c.processAI(ctx, batch)
}

func (c *Consumer) processHard(ctx context.Context, batch []models.RentPost) error {
	if len(batch) == 0 {
		return nil
	}
	log := pkglog.Component(pkglog.Filter)
	rules, err := c.rules()
	if err != nil {
		log.Error("规则读取失败", "count", len(batch), "err", err)
		return nil
	}
	for i := range batch {
		post := batch[i]
		res, tags, err := c.chain.EvaluateHard(ctx, post, rules)
		if err != nil {
			log.Error("硬链评估失败", "post_id", post.ID, "err", err)
			continue
		}
		if err := c.commitHard(res, tags); err != nil {
			return err
		}
	}
	return nil
}

func (c *Consumer) ReplayHard(ctx context.Context, batch []models.RentPost) error {
	if len(batch) == 0 {
		return nil
	}
	log := pkglog.Component(pkglog.Filter)
	rules, err := c.rules()
	if err != nil {
		log.Error("规则 replay 读规则失败", "count", len(batch), "err", err)
		return nil
	}
	n := 0
	for i := range batch {
		post := batch[i]
		res, tags, err := c.chain.EvaluateHard(ctx, post, rules)
		if err != nil {
			log.Error("规则 replay 评估失败", "post_id", post.ID, "err", err)
			continue
		}
		becamePassed := post.Status != models.PostStatusPassed && res.Status == models.PostStatusPassed
		if err := c.commitHard(res, tags); err != nil {
			return err
		}
		if becamePassed {
			if err := c.store.ClearNotificationsByPost(post.ID); err != nil {
				log.Error("重放通过后清通知账本失败", "post_id", post.ID, "err", err)
			}
		}
		n++
	}
	log.Info("规则 replay 完成", "count", len(batch), "changed", n)
	return nil
}

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
	if c.rt != nil {
		if ev, reason := LiveAIEvaluator(c.rt); ev == nil {
			log.Info(reason)
			return nil
		} else {
			c.chain.ai = ev
		}
	} else if !c.chain.HasAI() {
		return nil
	}
	if len(enabledAIRules(rules)) == 0 {
		log.Info("当前配置没有启用的 AI 规则，无需执行")
		return nil
	}
	for _, sub := range splitBatches(batch, c.aiSize()) {
		results, err := c.chain.EvaluateAIBatch(ctx, sub, rules)
		if err != nil {
			log.Warn("AI 批处理失败", "count", len(sub), "err", err)
			continue
		}
		for _, post := range sub {
			if err := c.commitAI(results[post.ID]); err != nil {
				return err
			}
		}
		log.Info("AI 批处理完成", "count", len(sub))
	}
	return nil
}

func (c *Consumer) aiSize() int {
	if c.rt != nil {
		if n := c.rt.Get().Filter.AIBatchSize; n > 0 {
			return n
		}
	}
	return c.aiBatchSize
}

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

// commitHard 硬规则定案：status + filter_results + system tags
func (c *Consumer) commitHard(res models.FilterResult, locations []string) error {
	if len(res.HardRules) > 0 || res.RejectedBy != "" || res.Status == models.PostStatusPassed {
		if err := c.store.SaveFilterResult(res); err != nil {
			pkglog.Component(pkglog.Filter).Error("筛选结果写库失败", "post_id", res.PostID, "err", err)
		}
	}
	if err := c.store.ReplaceSystemTags(res.PostID, SystemTagsFromHard(res, locations)); err != nil {
		pkglog.Component(pkglog.Filter).Error("系统标签写库失败", "post_id", res.PostID, "err", err)
	}
	if err := c.store.MarkStatus([]int64{res.PostID}, res.Status); err != nil {
		return err
	}
	pkglog.Component(pkglog.Filter).Info("帖子已定案", "post_id", res.PostID, "stage", res.Stage, "result", res.Status,
		"reason", res.RejectedBy)
	return nil
}

func (c *Consumer) commitAI(res models.FilterResult) error {
	existing, ok, err := c.store.FilterResultByPostID(res.PostID)
	if err != nil {
		pkglog.Component(pkglog.AIReview).Error("读已有筛选结果失败", "post_id", res.PostID, "err", err)
	} else if ok {
		res.HardRules = existing.HardRules
		res.Status = existing.Status
		res.RejectedBy = existing.RejectedBy
	} else {
		res.Status = models.PostStatusPassed
		res.RejectedBy = ""
	}
	if err := c.store.SaveFilterResult(res); err != nil {
		pkglog.Component(pkglog.AIReview).Error("筛选结果写库失败", "post_id", res.PostID, "err", err)
	}
	if res.AI != nil && res.AI.Price > 0 {
		if err := c.store.UpdatePostPrice(res.PostID, res.AI.Price); err != nil {
			pkglog.Component(pkglog.AIReview).Error("帖子价格写库失败", "post_id", res.PostID, "price", res.AI.Price, "err", err)
		}
	}
	if res.AI != nil && models.HasContact(res.AI.Contact) {
		if err := c.store.UpdatePostContact(res.PostID, res.AI.Contact); err != nil {
			pkglog.Component(pkglog.AIReview).Error("帖子联系方式写库失败", "post_id", res.PostID, "err", err)
		}
	}
	pkglog.Component(pkglog.AIReview).Info("AI 徽章已写入", "post_id", res.PostID, "ai_passed", res.AI != nil && res.AI.Passed,
		"ai_reason", aiReason(res.AI))
	return nil
}

func aiReason(ai *models.AIResult) string {
	if ai == nil {
		return ""
	}
	return ai.Reason
}

func (c *Consumer) rules() ([]models.Rule, error) {
	return c.store.ListRules(true)
}
