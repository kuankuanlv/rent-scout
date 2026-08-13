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

// CookieTestPath Cookie 草稿探测路径（auth / setupGate 豁免用）
const CookieTestPath = "/admin/config/cookie/test"

// LLMTestPath LLM 连通检测（草稿不写库）
const LLMTestPath = "/admin/config/llm/test"

// LLMModelsPath 拉取 OpenAI 兼容模型列表（草稿不写库）
const LLMModelsPath = "/admin/config/llm/models"

type configSection struct {
	ID    string
	Title string
	Desc  string
	Items []configField
}

type configField struct {
	Key          string
	Label        string
	Value        string
	Type         string   // text/number/password/textarea/checkbox/select/sources
	Hint         string
	CanClear     bool     // 敏感项可显式清空
	ShowWhen     string   // 联动显隐：空=始终；cookie 为 raw/cookiecloud；llm 为 openai/other（逗号=多选）
	Group        string   // 子 tab：通知 common/feishu/pushplus；采集 common/douban/weibo
	Options      []string // sources / select 选项（存库值）
	OptionLabels []string // 与 Options 等长的展示文案；空则显示 Options 原文
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
	"secret.filter.llm.api_style":        true,
	"secret.notifier.feishu.webhook":     true,
	"secret.notifier.dingtalk.webhook":   true,
	"secret.notifier.dingtalk.secret":    true,
	"secret.notifier.wecom.webhook":      true,
	"secret.notifier.pushplus.token":     true,
	"secret.notifier.serverchan.sendkey": true,
	"secret.notifier.webhook.url":        true,
	"secret.notifier.webhook.template":   true,
}

// ChangedRestartKeys 返回 updates 相对 before 实际变更的需重启 key（已排序）
func ChangedRestartKeys(before, updates map[string]string) []string {
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
	{ID: "ai", Title: "AI"},
	{ID: "notifier", Title: "通知"},
	{ID: "admin", Title: "管理"},
}

func normalizeConfigTab(tab string) string {
	switch tab {
	case "general", "sources", "rules", "ai", "notifier", "admin":
		return tab
	case "collector": // 旧内部名，映射到 sources
		return "sources"
	case "filter": // 旧「筛选」tab → AI
		return "ai"
	default:
		return "general"
	}
}

// tabToSectionID URL tab → KV SectionKeys 分区名；rules 无分区
func tabToSectionID(tab string) string {
	switch tab {
	case "sources":
		return "collector"
	case "ai":
		return "filter"
	default:
		return tab
	}
}

// sectionIDToTab 保存后 PRG 用：collector → sources；filter → ai
func sectionIDToTab(section string) string {
	switch section {
	case "collector":
		return "sources"
	case "filter":
		return "ai"
	default:
		return section
	}
}

func buildConfigSections(app *config.AppConfig, env *config.Secrets, kv map[string]string) []configSection {
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
	cookieRawHint := "粘贴 cookie 原文；留空不修改"
	if raw := env.Collector.Douban.CookieRaw; raw != "" {
		cookieRawHint = fmt.Sprintf("已保存 · 长度 %d；留空不修改", len(raw))
	}
		apiStyle := get("secret.filter.llm.api_style", env.Filter.LLM.APIStyle)
		apiStyle = normalizeLLMAPIStyle(apiStyle)
		if apiStyle == "" {
			// 未配风格时：AI 关 → none；否则 openai
			if ai == "false" {
				apiStyle = "none"
			} else {
				apiStyle = "openai"
			}
		}
		ccPass := get("secret.collector.douban.cookiecloud_password", env.Collector.Douban.CookiecloudPass)
			return []configSection{
				{
					ID: "general", Title: "常规", Desc: "服务运行基础参数",
					Items: []configField{
						{Key: "server.addr", Label: "监听地址", Value: get("server.addr", app.Server.Addr), Type: "text", Hint: "如 :7777；修改后需重启"},
						{Key: "log.level", Label: "日志级别", Value: get("log.level", app.Log.Level), Type: "text"},
						{Key: "log.path", Label: "日志文件", Value: get("log.path", app.Log.Path), Type: "text", Hint: "留空=stdout；修改后需重启"},
					},
				},
				{
					ID: "collector", Title: "采集", Desc: "信息源与 Cookie",
					Items: []configField{
						{Key: "collector.sources", Label: "启用源", Value: get("collector.sources", strings.Join(app.Collector.Sources, ",")), Type: "sources", Options: []string{"douban", "weibo"}, Hint: "多选；未实现源启动时 warn 跳过；修改后需重启", Group: "common"},
						{Key: "collector.interval", Label: "采集间隔(秒)", Value: get("collector.interval", strconv.Itoa(app.Collector.Interval)), Type: "number", Group: "common"},
						{Key: "collector.jitter_ratio", Label: "抖动比例", Value: get("collector.jitter_ratio", fmt.Sprintf("%g", app.Collector.JitterRatio)), Type: "text", Group: "common"},
						{Key: "collector.max_age_days", Label: "帖子时效(天)", Value: get("collector.max_age_days", strconv.Itoa(app.Collector.MaxAgeDays)), Type: "number", Hint: "非豆瓣源用；豆瓣以「拉取范围」为准", Group: "common"},
						{Key: "collector.douban.groups", Label: "豆瓣小组 URL", Value: get("collector.douban.groups", strings.Join(app.Collector.Douban.Groups, "\n")), Type: "textarea", Hint: "每行一个；修改后需重启", Group: "douban"},
						{Key: "collector.douban.interval", Label: "豆瓣间隔(秒)", Value: get("collector.douban.interval", strconv.Itoa(app.Collector.Douban.Interval)), Type: "number", Group: "douban"},
						{Key: "collector.douban.range_from", Label: "拉取范围（从）", Value: get("collector.douban.range_from", app.Collector.Douban.RangeFrom), Type: "text", Hint: "now=动态当前；-10d=动态十天前；也可填具体日期", Group: "douban"},
						{Key: "collector.douban.range_to", Label: "拉取范围（到）", Value: get("collector.douban.range_to", app.Collector.Douban.RangeTo), Type: "text", Hint: "now=动态当前；-10d=动态十天前；也可填具体日期", Group: "douban"},
						{Key: "secret.collector.douban.cookie_mode", Label: "Cookie 模式", Value: get("secret.collector.douban.cookie_mode", env.Collector.Douban.CookieMode), Type: "select", Options: []string{"none", "raw", "cookiecloud"}, Hint: "none / raw / cookiecloud", Group: "douban"},
						{Key: "secret.collector.douban.cookie_raw", Label: "Cookie 原文", Value: "", Type: "textarea", CanClear: true, Hint: cookieRawHint, ShowWhen: "raw", Group: "douban"},
						{Key: "secret.collector.douban.cookiecloud_url", Label: "CookieCloud 地址", Value: get("secret.collector.douban.cookiecloud_url", env.Collector.Douban.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com", CanClear: true, ShowWhen: "cookiecloud", Group: "douban"},
						{Key: "secret.collector.douban.cookiecloud_key", Label: "CookieCloud UUID", Value: get("secret.collector.douban.cookiecloud_key", env.Collector.Douban.CookiecloudKey), Type: "text", CanClear: true, ShowWhen: "cookiecloud", Group: "douban"},
						{Key: "secret.collector.douban.cookiecloud_password", Label: "CookieCloud 密码", Value: ccPass, Type: "password", CanClear: true, Hint: "已回显已存密码；勾选清空可删除", ShowWhen: "cookiecloud", Group: "douban"},
					},
				},
				{
					ID: "filter", Title: "AI", Desc: "本配置用于审核帖子，并会记录审核通过/拒绝的具体原因。",
					Items: []configField{
						{Key: "secret.filter.llm.api_style", Label: "LLM 提供方", Value: apiStyle, Type: "select", Options: []string{"none", "openai", "other"}, OptionLabels: []string{"无", "OpenAI", "其他"}, Hint: "选「无」关闭 AI；OpenAI/其他需填 endpoint 与密钥"},
						{Key: "filter.batch_size", Label: "组批大小", Value: get("filter.batch_size", strconv.Itoa(app.Filter.BatchSize)), Type: "number", Hint: "筛选单次从库拉取处理的最大条数（积压够了就开干）"},
						{Key: "filter.ai_batch_size", Label: "AI 批大小", Value: get("filter.ai_batch_size", strconv.Itoa(app.Filter.AIBatchSize)), Type: "number", ShowWhen: "openai,other"},
						{Key: "secret.filter.llm.base_url", Label: "Base URL", Value: get("secret.filter.llm.base_url", env.Filter.LLM.BaseURL), Type: "text", CanClear: true, Hint: "如 https://api.openai.com/v1；修改后需重启", ShowWhen: "openai,other"},
						{Key: "secret.filter.llm.api_key", Label: "API Key", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启", ShowWhen: "openai,other"},
						{Key: "secret.filter.llm.model", Label: "主模型", Value: get("secret.filter.llm.model", env.Filter.LLM.Model), Type: "text", CanClear: true, Hint: "修改后需重启", ShowWhen: "openai,other"},
						{Key: "secret.filter.llm.fallback_models", Label: "Fallback 模型", Value: get("secret.filter.llm.fallback_models", strings.Join(env.Filter.LLM.FallbackModels, ",")), Type: "textarea", CanClear: true, Hint: "逗号或换行分隔；失败时按序回退", ShowWhen: "openai,other"},
					},
				},
			{
				ID: "notifier", Title: "通知", Desc: "飞书 / PushPlus",
				Items: []configField{
					{Key: "notifier.batch_size", Label: "组批大小", Value: get("notifier.batch_size", strconv.Itoa(app.Notifier.BatchSize)), Type: "number", Hint: "通知单次从库拉取处理的最大条数", Group: "common"},
					{Key: "notifier.max_attempts", Label: "最大重试", Value: get("notifier.max_attempts", strconv.Itoa(app.Notifier.MaxAttempts)), Type: "number", Group: "common"},
					{Key: "notifier.retry_base_interval", Label: "重试间隔(秒)", Value: get("notifier.retry_base_interval", strconv.Itoa(app.Notifier.RetryBaseInterval)), Type: "number", Group: "common"},
					{Key: "secret.notifier.feishu.webhook", Label: "飞书 Webhook", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启服务", Group: "feishu"},
					{Key: "secret.notifier.pushplus.token", Label: "PushPlus Token", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启服务", Group: "pushplus"},
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

// ParseSectionForm 从表单提取分区更新（敏感空值沿用 keepSecrets）
func ParseSectionForm(form url.Values, section string, keepSecrets map[string]string) map[string]string {
	allowed := map[string]bool{}
	for _, k := range config.SectionKeys[section] {
		allowed[k] = true
	}
	updates := map[string]string{}
	for key, values := range form {
		if !allowed[key] || len(values) == 0 {
			continue
		}
		if form.Get("clear_"+key) == "on" {
			updates[key] = config.EmptySentinel
			continue
		}
		v := values[0]
		if key == "collector.sources" || key == "secret.filter.llm.fallback_models" {
			v = joinFormValues(values)
		}
		v = config.NormalizeValue(v)
		if key == "secret.filter.llm.fallback_models" {
			// textarea 可能用换行，统一成逗号
			v = strings.Join(splitFlexible(v), ",")
		}
		if key == "secret.filter.llm.api_style" {
			v = normalizeLLMAPIStyle(v)
		}
		if strings.HasPrefix(key, "secret.") && (v == "" || v == "••••••••") {
			if old, ok := keepSecrets[key]; ok {
				updates[key] = old
			}
			continue
		}
		updates[key] = v
	}
	// 启用源多选：未勾选时显式写空（交给校验报错）
	if allowed["collector.sources"] {
		updates["collector.sources"] = joinFormValues(form["collector.sources"])
	}
	// api_style 驱动 ai_enabled：无=关，openai/other=开（不再依赖单独 checkbox）
	if allowed["secret.filter.llm.api_style"] {
		style := normalizeLLMAPIStyle(updates["secret.filter.llm.api_style"])
		if style == "" {
			style = normalizeLLMAPIStyle(form.Get("secret.filter.llm.api_style"))
		}
		if style == "" {
			style = "none"
		}
		updates["secret.filter.llm.api_style"] = style
		if allowed["filter.ai_enabled"] {
			if style == "none" {
				updates["filter.ai_enabled"] = "false"
			} else {
				updates["filter.ai_enabled"] = "true"
			}
		}
	}
	if allowed["admin.auth_required"] {
		if form.Get("admin.auth_required") == "on" {
			updates["admin.auth_required"] = "true"
		} else {
			updates["admin.auth_required"] = "false"
		}
	}
	return updates
}

// normalizeLLMAPIStyle 旧 custom → other；非法空串原样
func normalizeLLMAPIStyle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "custom" {
		return "other"
	}
	return s
}

func joinFormValues(values []string) string {
	var parts []string
	for _, x := range values {
		for _, p := range splitFlexible(x) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ",")
}

func splitFlexible(s string) []string {
	s = strings.ReplaceAll(s, "\n", ",")
	s = strings.ReplaceAll(s, "，", ",")
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// CurrentConfigKV 读库 KV；空则回落默认 App+Env
func CurrentConfigKV(db *store.Store) map[string]string {
	kv, err := store.GetConfigMap(db)
	if err != nil || len(kv) == 0 {
		return config.MergeKV(config.AppToKV(config.DefaultApp()), config.SecretsToKV(config.DefaultSecrets()))
	}
	return kv
}

// MergeDefaultsInto 把默认值填进 updates（库里还没有的 key）
func MergeDefaultsInto(updates map[string]string, kv map[string]string) {
	def := config.MergeKV(config.AppToKV(config.DefaultApp()), config.SecretsToKV(config.DefaultSecrets()))
	for k, v := range def {
		if _, inKV := kv[k]; !inKV {
			if _, set := updates[k]; !set {
				updates[k] = v
			}
		}
	}
}

func (s *Server) saveSectionUpdates(section string, updates map[string]string) error {
	if len(updates) == 0 {
		return fmt.Errorf("没有有效的配置项")
	}
	for k, v := range updates {
		updates[k] = config.NormalizeValue(v)
	}
	current := CurrentConfigKV(s.db)
	merged := config.MergeKV(current, updates)
	app := config.KVToApp(merged)
	if errs := config.ValidateApp(app); len(errs) > 0 {
		return fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	env := config.KVToSecrets(merged)
	if errs := config.ValidateSecrets(env); len(errs) > 0 {
		return fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	if err := store.SetConfigBatch(s.db, updates); err != nil {
		return err
	}
	return s.rt.ReloadOnce()
}
