package collector

import (
	"context"
	"fmt"
	"os"
	"strings"

	"rent-scout/internal/config"
)

// CookieProvider cookie 获取层（规格 4.4）：按源返回可用 cookie。
// 三种实现：none（匿名）/ file（本地文件暂存）/ cookiecloud（同步）
type CookieProvider interface {
	Get(ctx context.Context, source string) (string, error)
}

// NewCookieProvider 按 cookie_mode 选择实现；file 需传文件路径
func NewCookieProvider(mode, cookieFile string, cfg config.DoubanCookieConfig) (CookieProvider, error) {
	switch mode {
	case "", "none":
		return noneProvider{}, nil
	case "file":
		return fileProvider{path: cookieFile}, nil
	case "cookiecloud":
		// 任务 8 实现 CookieCloud 同步；当前显式报错，不静默降级
		return nil, fmt.Errorf("cookiecloud 待实现（任务 8）")
	default:
		return nil, fmt.Errorf("未知 cookie_mode: %q", mode)
	}
}

// noneProvider 匿名访问
type noneProvider struct{}

func (noneProvider) Get(ctx context.Context, source string) (string, error) {
	return "", nil
}

// fileProvider 读本地 cookie 文件（手动导出/脚本更新，即改即用）；
// 文件缺失/读失败 → 降级匿名（不阻断采集，规格 4.4）
type fileProvider struct {
	path string
}

func (p fileProvider) Get(ctx context.Context, source string) (string, error) {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return "", nil // 降级匿名
	}
	return strings.TrimSpace(string(b)), nil
}
