package notifier

import (
	"context"
	"fmt"
	"sort"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// NotifierOptions 通知重试参数（规格 6.6）
type NotifierOptions struct {
	MaxAttempts       int               // 单渠道失败重试次数（超过进死信）；<=0 用 3
	RetryBaseInterval int               // 重试退避基础间隔（秒）；<=0 用 300
	HotConfig         *config.HotConfig // 签名密钥每次从 HotConfig 读；优先于 FeedbackSecret
	FeedbackSecret    string            // 仅测试兜底；生产应注入 HotConfig
}

// Notifier 通知消费器：按渠道过滤未 sent → 地址分组 → 每组 Send → 状态写库（规格 6.5/6.6）
type Notifier struct {
	st       *store.Store
	channels []Channel
	opts     NotifierOptions
}

// NewNotifier 创建通知消费器；channels 为启用渠道（至少一个）
func NewNotifier(st *store.Store, opts NotifierOptions, channels ...Channel) *Notifier {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.RetryBaseInterval <= 0 {
		opts.RetryBaseInterval = 300
	}
	return &Notifier{st: st, channels: channels, opts: opts}
}

// currentFeedbackSecret 每次签名读 HotConfig 当前 admin token；鉴权关则空（规格 7.1）
func (n *Notifier) currentFeedbackSecret() string {
	if n.opts.HotConfig != nil {
		return n.opts.HotConfig.FeedbackSecret()
	}
	return n.opts.FeedbackSecret
}

// ProcessBatch 处理一批 passed 帖子（pipeline.BatchFunc）：
// 拉批内渠道状态 → 按渠道过滤 → 地址分组 → 每组 Send → 写状态（sent/failed/dead）
func (n *Notifier) ProcessBatch(ctx context.Context, batch []models.RentPost) error {
	if len(batch) == 0 || len(n.channels) == 0 {
		return nil
	}
	log := pkglog.Component(pkglog.Notifier)
	log.Info("收到通知触发", "count", len(batch))
	ids := make([]int64, len(batch))
	for i, p := range batch {
		ids[i] = p.ID
	}
	chanNames := make([]string, len(n.channels))
	for i, c := range n.channels {
		chanNames[i] = c.Name()
	}
	statuses, err := n.st.NotificationStatuses(ids, chanNames)
	if err != nil {
		return fmt.Errorf("查询通知状态: %w", err)
	}

	var firstErr error
	for _, ch := range n.channels {
		if err := n.sendChannel(ctx, ch, batch, statuses); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sendChannel 单渠道发送：过滤 sent/dead 帖 → 地址分组（tag 排序保证顺序稳定）→ 每组 Send → 状态写库
func (n *Notifier) sendChannel(ctx context.Context, ch Channel, batch []models.RentPost,
	statuses map[int64]map[string]string) error {

	var pending []models.RentPost
	for _, p := range batch {
		st := statuses[p.ID][ch.Name()]
		if st == "sent" || st == "dead" {
			continue // sent 已发过；dead 人工处理，不再自动发
		}
		pending = append(pending, p)
	}
	if len(pending) == 0 {
		return nil
	}
	groups := groupByAddressTag(pending)
	tags := make([]string, 0, len(groups))
	for tag := range groups {
		tags = append(tags, tag)
	}
	sort.Strings(tags) // 稳定发送顺序（测试确定性 + 组隔离）

	var firstErr error
	for _, tag := range tags {
		if err := n.sendGroup(ctx, ch, tag, groups[tag]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sendGroup 单组发送：构造 NotifyItem（先幂等建记录，失败跳过该帖）→ Send → 逐帖写状态。
// 整组失败/成功路径均遍历 items（与 Send 的 failed 列表同序——items 已按 sortByPriority 排序）
func (n *Notifier) sendGroup(ctx context.Context, ch Channel, tag string, posts []models.RentPost) error {
	items := make([]NotifyItem, 0, len(posts))
	for _, p := range posts {
		// 幂等建记录（已存在则忽略；attempts 从 0 起，失败时 +1）。
		// 建记录失败：跳过该帖不发送（避免状态记录缺失导致下一轮重复通知）
		if _, err := n.st.InsertNotification(p.ID, ch.Name()); err != nil {
			pkglog.Component(pkglog.Notifier).Error("通知账本建记录失败", "post_id", p.ID, "channel", ch.Name(), "err", err)
			continue
		}
		secret := n.currentFeedbackSecret()
		item := NotifyItem{
			PostID:             p.ID,
			Title:              p.Title,
			URL:                p.URL,
			AddressTag:         tag,
			FeedbackURL:        BuildFeedbackURL(p.ID, "useful", secret),
			FeedbackUselessURL: BuildFeedbackURL(p.ID, "useless", secret),
			HandledURL:         BuildFeedbackURL(p.ID, "handled", secret),
		}
		// 展示字段来自 filter_results（价格/联系人/通勤/理由）
		if fr, ok, err := n.st.FilterResultByPostID(p.ID); err == nil && ok && fr.AI != nil {
			item.Price = fr.AI.Price
			item.Contact = fr.AI.Contact
			item.Commuting = fr.AI.Commuting
			item.Reason = fr.AI.Reason
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}
	items = sortByPriority(items)

	log := pkglog.Component(pkglog.Notifier)
	log.Info("渠道分组发送", "channel", ch.Name(), "group", tag, "items", len(items))
	sent, failed, err := ch.Send(ctx, items)
	if err != nil && len(sent) == 0 {
		// 整组失败：按 items 下标逐帖标记 failed（attempts+1，达阈值 dead）——items 与 failed 同序
		for i, it := range items {
			n.recordOutcome(it.PostID, ch.Name(), failedFor(i, failed, err))
		}
		return err
	}
	sentSet := map[int64]bool{}
	for _, id := range sent {
		sentSet[id] = true
	}
	for _, it := range items {
		if sentSet[it.PostID] {
			if err := n.st.MarkNotificationSent(it.PostID, ch.Name()); err != nil {
				log.Error("标记已发送失败", "post_id", it.PostID, "channel", ch.Name(), "err", err)
			}
			log.Info("单条通知已发送", "channel", ch.Name(), "post_id", it.PostID, "status", "sent")
			continue
		}
		n.recordOutcome(it.PostID, ch.Name(), nil)
	}
	return nil
}

// recordOutcome 写失败状态：attempts+1；达 MaxAttempts → dead（规格 6.6）
func (n *Notifier) recordOutcome(postID int64, channel string, sendErr error) {
	log := pkglog.Component(pkglog.Notifier)
	attempt, err := n.st.NotificationAttempts(postID, channel)
	if err != nil {
		log.Error("读取重试次数失败", "post_id", postID, "channel", channel, "err", err)
		attempt = 0
	}
	attempt++
	msg := "发送失败"
	if sendErr != nil {
		msg = sendErr.Error()
	}
	if attempt >= n.opts.MaxAttempts {
		if err := n.st.MarkNotificationDead(postID, channel, msg); err != nil {
			log.Error("标记死信失败", "post_id", postID, "channel", channel, "err", err)
		}
		log.Warn("进入死信", "channel", channel, "post_id", postID, "moved_to", "dead")
		return
	}
	if err := n.st.MarkNotificationFailed(postID, channel, msg, attempt); err != nil {
		log.Error("标记发送失败态失败", "post_id", postID, "channel", channel, "err", err)
	}
	log.Warn("发送失败可重试", "channel", channel, "post_id", postID, "attempt", attempt, "status", "failed")
}

// failedFor 从 Send 返回的 failed 列表中取第 idx 项错误（与 items 同序）；越界/缺失用 err
func failedFor(idx int, failed []error, err error) error {
	if idx >= 0 && idx < len(failed) && failed[idx] != nil {
		return failed[idx]
	}
	return err
}
