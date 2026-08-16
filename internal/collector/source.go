package collector

import (
	"context"
	"fmt"
	"time"

	"rent-scout/internal/models"
)

// ListItem 列表页条目（调整规格 E 分层）：轻量信息，Runner 据此
// 做时间窗过滤与批量查重，仅新帖才调 Detail
type ListItem struct {
	ExternalID  string // 源内唯一 ID（详情页/去重键依据）
	URL         string // 详情页链接
	Title       string
	Author      string
	AuthorID    string    // 博主 uid，评论兜底比对用
	MblogID     string    // 微博 mblogid，长文接口用
	Kind        string    // 微博渠道：user / super
	PublishedAt time.Time // 源发布时间（窗口过滤依据）
	Content     string    // 列表页已带正文时 Detail 可直接用；豆瓣留空
	NeedDetail  bool      // 列表是截断摘要，Detail 再拉全文（微博长微博）
}

// Source 信息源适配器（规格 4.2 修订版）：List/Detail 双层。
// 适配器不依赖 store——查重编排在 Runner（调整规格 E）
type Source interface {
	Name() string
	// List 从 cursor 抓一页列表；返回条目 + 下一页游标（"" = 无更多页）。
	// 条目按时间倒序（豆瓣语义）；超窗停止由 Runner 判断
	List(ctx context.Context, cursor string) ([]ListItem, string, error)
	// Detail 抓取单条详情页并归一化为 RentPost（只对未存在的新帖调用）
	Detail(ctx context.Context, item ListItem) (models.RentPost, error)
}

// GroupSkipper 多组源可选：本组已撞水位/时间窗时跳到下一组开头，少打无效页
type GroupSkipper interface {
	SkipGroup(cursor string) string
}

// CursorDescriber 把游标说成人话（超话第2页）；没有则 Runner 用组N第M页
type CursorDescriber interface {
	DescribeCursor(cursor string) string
}

// TimeWindowLister 列表请求能带时间窗（博主 searchProfile、超话 feed）
type TimeWindowLister interface {
	ListInWindow(ctx context.Context, cursor string, start, end time.Time) ([]ListItem, string, error)
}

func skipGroup(src Source, cursor string) string {
	if g, ok := src.(GroupSkipper); ok {
		return g.SkipGroup(cursor)
	}
	return ""
}

// GroupWatermarker 多组源可选：水位按稳定键分开记；无序组不写水位、也不早停
type GroupWatermarker interface {
	WatermarkKey(cursor string) string
	TimeOrdered(cursor string) bool
}

func watermarkMeta(src Source, cursor string) (key string, ordered bool) {
	if w, ok := src.(GroupWatermarker); ok {
		return w.WatermarkKey(cursor), w.TimeOrdered(cursor)
	}
	g, _ := parsePageCursor(cursor)
	return fmt.Sprintf("g:%d", g), true
}
