package collector

import (
	"context"
	"errors"
	"time"

	"rent-scout/internal/models"
)

// ErrUnrecoverable Cookie 挂了这类修不好的错，外层会冷却 1 小时
var ErrUnrecoverable = errors.New("collector: unrecoverable error")

// ListItem 列表页轻量条目，Detail 前先用它去重
type ListItem struct {
	ExternalID  string
	URL         string
	Title       string
	Author      string
	AuthorID    string
	MblogID     string
	Kind        string
	PublishedAt time.Time
	Content     string
	NeedDetail  bool
}

// Iterator 翻页黑盒：Next 拉一页，Checkpoint 存进度，Err 报硬错
type Iterator interface {
	Next(ctx context.Context) bool
	Value() []ListItem
	Checkpoint() string
	Err() error
}

// Source 数据源：按状态+时间窗开迭代器，新帖再补 Detail
type Source interface {
	Name() string
	NewIterator(state string, start, end time.Time) Iterator
	Detail(ctx context.Context, item ListItem) (models.RentPost, error)
}
