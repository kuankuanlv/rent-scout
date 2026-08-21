package collector_test

import (
	"slices"
	"testing"

	"rent-scout/internal/collector"
	"rent-scout/internal/collector/sources/douban"
	"rent-scout/internal/collector/sources/weibo"
	"rent-scout/internal/collector/sources/xiaohongshu"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

func TestXiaohongshuIsRegisteredButDisabledByDefault(t *testing.T) {
	// 模拟初始化
	rt := config.NewHotConfigWithSnapshot(config.DefaultApp(), nil)
	
	// 初始化 Sources
	// 注意：这里需要真实的 Source 实现，不引入循环依赖
	sources := []collector.Source{
		&douban.Douban{},
		&weibo.Weibo{},
		&xiaohongshu.Xiaohongshu{},
	}
	
	runner := collector.NewRunner(rt, nil, sources, nil)
	
	app := rt.Get()
	if slices.Contains(app.Collector.Sources, models.SourceXiaohongshu.String()) {
		t.Fatal("new source must be opt-in")
	}
	
	// 验证已注册
	found := false
	for _, s := range runner.Sources() {
		if s == models.SourceXiaohongshu.String() {
			found = true
			break
		}
	}
	if !found {
		t.Error("xiaohongshu source should be registered in runner")
	}
}
