package admin

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

type configSection struct {
	ID    string
	Title string
	Desc  string
	Items []configField
}

type configField struct {
	Key      string
	Label    string
	Value    string
	Type     string
	Hint     string
	CanClear bool // 敏感项可显式清空
}

// RestartKeys 改后需重启才生效（与 Spec 热加载矩阵一致）；admin.token 热生效不在此列
var RestartKeys = map[string]bool{
	"server.addr":                        true,
	"log.path":                           true,
	"collector.sources":                  true,
	"collector.douban.groups":            true, // 与 sources 同级启动冻结
	"secret.filter.llm.api_key":          true,
	"secret.filter.llm.base_url":         true,
	"secret.filter.llm.model":            true,
	"secret.filter.llm.fallback_models":  true,
	"secret.notifier.feishu.webhook":     true,
	"secret.notifier.dingtalk.webhook":   true,
	"secret.notifier.dingtalk.secret":    true,
	"secret.notifier.wecom.webhook":      true,
	"secret.notifier.pushplus.token":     true,
	"secret.notifier.serverchan.sendkey": true,
	"secret.notifier.webhook.url":        true,
	"secret.notifier.webhook.template":   true,
}

// changedRestartKeys 返回 updates 相对 before 实际变更的需重启 key（已排序）
func changedRestartKeys(before, updates map[string]string) []string {
	var keys []string
	for k, v := range updates {
		if !RestartKeys[k] {
			continue
		}
		if before[k] != v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// configTab 配置页二级 Tab（URL ?tab=）；sources 对应内部 collector 分区
type configTab struct {
	ID    string
	Title string
}

var configTabs = []configTab{
	{ID: "general", Title: "常规"},
	{ID: "sources", Title: "采集"},
	{ID: "rules", Title: "规则"},
	{ID: "filter", Title: "筛选"},
	{ID: "notifier", Title: "通知"},
	{ID: "admin", Title: "管理"},
}

func normalizeConfigTab(tab string) string {
	switch tab {
	case "general", "sources", "rules", "filter", "notifier", "admin":
		return tab
	case "collector": // 旧内部名，映射到 sources
		return "sources"
	default:
		return "general"
	}
}

// tabToSectionID URL tab → KV SectionKeys 分区名；rules 无分区
func tabToSectionID(tab string) string {
	if tab == "sources" {
		return "collector"
	}
	return tab
}

// sectionIDToTab 保存后 PRG 用：collector → sources
func sectionIDToTab(section string) string {
	if section == "collector" {
		return "sources"
	}
	return section
}

func buildConfigSections(app *config.AppConfig, env *config.EnvLocalConfig, kv map[string]string) []configSection {
	get := func(key, def string) string {
		if v, ok := kv[key]; ok {
			return v
		}
		return def
	}
	ai := "true"
	if app.Filter.AIEnabled != nil && !*app.Filter.AIEnabled {
		ai = "false"
	}
	auth := "false"
	if app.Admin.AuthRequired {
		auth = "true"
	}
	trimDouban := "800"
	if app.Filter.TrimLimits != nil {
		if n, ok := app.Filter.TrimLimits["douban"]; ok {
			trimDouban = strconv.Itoa(n)
		}
	}
	cookieRawHint := "粘贴 cookie 原文；留空不修改"
	if raw := env.Collector.Douban.CookieRaw; raw != "" {
		cookieRawHint = fmt.Sprintf("已保存 · 长度 %d；留空不修改", len(raw))
	}
	return []configSection{
		{
			ID: "general", Title: "常规", Desc: "服务运行基础参数",
			Items: []configField{
				{Key: "server.addr", Label: "监听地址", Value: get("server.addr", app.Server.Addr), Type: "text", Hint: "如 :7777；修改后需重启"},
				{Key: "log.level", Label: "日志级别", Value: get("log.level", app.Log.Level), Type: "text"},
				{Key: "log.path", Label: "日志文件", Value: get("log.path", app.Log.Path), Type: "text", Hint: "留空=stdout；修改后需重启"},
				{Key: "pipeline.batch_size", Label: "组批大小", Value: get("pipeline.batch_size", strconv.Itoa(app.Pipeline.BatchSize)), Type: "number"},
				{Key: "pipeline.linger_interval", Label: "兜底间隔(秒)", Value: get("pipeline.linger_interval", strconv.Itoa(app.Pipeline.LingerInterval)), Type: "number"},
			},
		},
		{
			ID: "collector", Title: "采集", Desc: "信息源与 Cookie",
			Items: []configField{
				{Key: "collector.sources", Label: "启用源", Value: get("collector.sources", strings.Join(app.Collector.Sources, ",")), Type: "text", Hint: "修改后需重启"},
				{Key: "collector.interval", Label: "采集间隔(秒)", Value: get("collector.interval", strconv.Itoa(app.Collector.Interval)), Type: "number"},
				{Key: "collector.jitter_ratio", Label: "抖动比例", Value: get("collector.jitter_ratio", fmt.Sprintf("%g", app.Collector.JitterRatio)), Type: "text"},
				{Key: "collector.max_age_days", Label: "帖子时效(天)", Value: get("collector.max_age_days", strconv.Itoa(app.Collector.MaxAgeDays)), Type: "number"},
				{Key: "collector.douban.groups", Label: "豆瓣小组 URL", Value: get("collector.douban.groups", strings.Join(app.Collector.Douban.Groups, "\n")), Type: "textarea", Hint: "每行一个；修改后需重启"},
				{Key: "collector.douban.interval", Label: "豆瓣间隔(秒)", Value: get("collector.douban.interval", strconv.Itoa(app.Collector.Douban.Interval)), Type: "number"},
				{Key: "secret.collector.douban.cookie_mode", Label: "Cookie 模式", Value: get("secret.collector.douban.cookie_mode", env.Collector.Douban.CookieMode), Type: "select", Hint: "none/raw/file/cookiecloud"},
				{Key: "secret.collector.douban.cookie_raw", Label: "Cookie 原文", Value: "", Type: "textarea", CanClear: true, Hint: cookieRawHint},
				{Key: "secret.collector.douban.cookie_file", Label: "Cookie 文件路径", Value: get("secret.collector.douban.cookie_file", env.Collector.Douban.CookieFile), Type: "text", CanClear: true},
				{Key: "secret.collector.douban.cookiecloud_url", Label: "CookieCloud 地址", Value: get("secret.collector.douban.cookiecloud_url", env.Collector.Douban.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com", CanClear: true},
				{Key: "secret.collector.douban.cookiecloud_key", Label: "CookieCloud UUID", Value: get("secret.collector.douban.cookiecloud_key", env.Collector.Douban.CookiecloudKey), Type: "text", CanClear: true},
				{Key: "secret.collector.douban.cookiecloud_password", Label: "CookieCloud 密码", Value: "", Type: "password", CanClear: true},
			},
		},
		{
			ID: "filter", Title: "筛选", Desc: "AI 与 LLM",
			Items: []configField{
				{Key: "filter.ai_enabled", Label: "启用 AI", Value: get("filter.ai_enabled", ai), Type: "checkbox"},
				{Key: "filter.ai_batch_size", Label: "AI 批大小", Value: get("filter.ai_batch_size", strconv.Itoa(app.Filter.AIBatchSize)), Type: "number"},
				{Key: "filter.trim_limits.douban", Label: "豆瓣截断字数", Value: get("filter.trim_limits.douban", trimDouban), Type: "number"},
				{Key: "secret.filter.llm.api_key", Label: "LLM API Key", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启"},
				{Key: "secret.filter.llm.base_url", Label: "LLM Base URL", Value: get("secret.filter.llm.base_url", env.Filter.LLM.BaseURL), Type: "text", CanClear: true, Hint: "修改后需重启"},
				{Key: "secret.filter.llm.model", Label: "LLM 模型", Value: get("secret.filter.llm.model", env.Filter.LLM.Model), Type: "text", CanClear: true, Hint: "修改后需重启"},
			},
		},
		{
			ID: "notifier", Title: "通知", Desc: "推送渠道（后续可扩展）",
			Items: []configField{
				{Key: "notifier.max_attempts", Label: "最大重试", Value: get("notifier.max_attempts", strconv.Itoa(app.Notifier.MaxAttempts)), Type: "number"},
				{Key: "notifier.retry_base_interval", Label: "重试间隔(秒)", Value: get("notifier.retry_base_interval", strconv.Itoa(app.Notifier.RetryBaseInterval)), Type: "number"},
				{Key: "secret.notifier.feishu.webhook", Label: "飞书 Webhook", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启服务"},
			},
		},
		{
			ID: "admin", Title: "管理", Desc: "控制台鉴权",
			Items: []configField{
				{Key: "admin.auth_required", Label: "启用鉴权", Value: get("admin.auth_required", auth), Type: "checkbox"},
				{Key: "admin.token", Label: "访问 Token", Value: "", Type: "password", CanClear: true},
			},
		},
	}
}

func sectionByID(sections []configSection, id string) *configSection {
	for i := range sections {
		if sections[i].ID == id {
			return &sections[i]
		}
	}
	return nil
}

func parseSectionForm(form url.Values, section string, keepSecrets map[string]string) map[string]string {
	allowed := map[string]bool{}
	for _, k := range config.SectionKeys[section] {
		allowed[k] = true
	}
	updates := map[string]string{}
	for key, values := range form {
		if !allowed[key] || len(values) == 0 {
			continue
		}
		v := values[0]
		if form.Get("clear_"+key) == "on" {
			updates[key] = config.EmptySentinel
			continue
		}
		v = config.NormalizeValue(v)
		if strings.HasPrefix(key, "secret.") && (v == "" || v == "••••••••") {
			if old, ok := keepSecrets[key]; ok {
				updates[key] = old
			}
			continue
		}
		updates[key] = v
	}
	for _, key := range []string{"filter.ai_enabled", "admin.auth_required"} {
		if !allowed[key] {
			continue
		}
		if form.Get(key) == "on" {
			updates[key] = "true"
		} else if _, ok := form[key]; ok {
			updates[key] = "false"
		}
	}
	if allowed["filter.ai_enabled"] && form.Get("filter.ai_enabled") == "" {
		updates["filter.ai_enabled"] = "false"
	}
	if allowed["admin.auth_required"] && form.Get("admin.auth_required") == "" {
		updates["admin.auth_required"] = "false"
	}
	return updates
}

func (s *Server) saveSectionUpdates(section string, updates map[string]string) error {
	if len(updates) == 0 {
		return fmt.Errorf("没有有效的配置项")
	}
	for k, v := range updates {
		updates[k] = config.NormalizeValue(v)
	}
	current := s.currentConfigKV()
	merged := config.MergeKV(current, updates)
	app := config.KVToApp(merged)
	if errs := config.ValidateApp(app); len(errs) > 0 {
		return fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	env := config.KVToEnv(merged)
	if errs := config.ValidateEnv(env); len(errs) > 0 {
		return fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	if err := store.SetConfigBatch(s.db, updates); err != nil {
		return err
	}
	return s.rt.ReloadOnce()
}

func (s *Server) currentConfigKV() map[string]string {
	kv, err := store.GetConfigMap(s.db)
	if err != nil || len(kv) == 0 {
		return config.MergeKV(config.AppToKV(config.DefaultApp()), config.EnvToKV(config.DefaultEnv()))
	}
	return kv
}

func mergeDefaultsInto(updates map[string]string, kv map[string]string) {
	def := config.MergeKV(config.AppToKV(config.DefaultApp()), config.EnvToKV(config.DefaultEnv()))
	for k, v := range def {
		if _, inKV := kv[k]; !inKV {
			if _, set := updates[k]; !set {
				updates[k] = v
			}
		}
	}
}
