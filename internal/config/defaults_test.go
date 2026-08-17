package config

import (
	"strings"
	"testing"
)

func TestDefaultKV(t *testing.T) {
	kv := DefaultKV()

	// 敏感键必须全空
	for _, k := range []string{
		"secret.collector.douban.cookie_raw",
		"secret.collector.douban.cookiecloud_url",
		"secret.collector.douban.cookiecloud_key",
		"secret.collector.douban.cookiecloud_password",
		"secret.collector.weibo.cookie_raw",
		"secret.collector.weibo.cookiecloud_url",
		"secret.collector.weibo.cookiecloud_key",
		"secret.collector.weibo.cookiecloud_password",
		"secret.filter.llm.api_key",
		"secret.notifier.feishu.webhook",
		"secret.notifier.dingtalk.webhook",
		"secret.notifier.wecom.webhook",
		"secret.notifier.pushplus.token",
		"secret.notifier.serverchan.sendkey",
		"secret.notifier.webhook.url",
	} {
		if kv[k] != "" {
			t.Errorf("%s 应为空，got %q", k, kv[k])
		}
	}

	// LLM 默认 DeepSeek
	if kv["secret.filter.llm.base_url"] != "https://api.deepseek.com" {
		t.Errorf("base_url = %q, want deepseek", kv["secret.filter.llm.base_url"])
	}
	if kv["secret.filter.llm.model"] != "deepseek-chat" {
		t.Errorf("model = %q, want deepseek-chat", kv["secret.filter.llm.model"])
	}

	// 反解一致性：KVToApp / KVToSecrets 不应报错，且组与源可解析
	app := KVToApp(kv)
	_ = KVToSecrets(kv)
	if len(app.Collector.Sources) != 2 {
		t.Errorf("sources = %v, want douban,weibo", app.Collector.Sources)
	}
	if len(app.Collector.Weibo.Users) < 1 || len(app.Collector.Weibo.SuperTopics) < 1 {
		t.Errorf("weibo users/supertopics 不应为空")
	}

	// 排除键
	for _, k := range []string{
		"admin.auth_required", "admin.token", "setup.completed",
		"posts.status_v3", "rules.defaults_version",
		"pipeline.batch_size", "pipeline.linger_interval",
		"collector.weibo.tags", "collector.weibo.urls",
		"filter.trim_limits.douban",
	} {
		if _, ok := kv[k]; ok {
			t.Errorf("不应包含键 %s", k)
		}
	}

	// 微博仅超话+博主结构（无普通话题键已由上面排除断言覆盖）；含注释行示例的小组
	if !strings.Contains(kv["collector.douban.groups"], "#北京租房") {
		t.Errorf("douban groups 应含示例小组")
	}
}
