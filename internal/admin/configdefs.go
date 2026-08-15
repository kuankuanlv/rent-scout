package admin

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// CookieTestPath 豆瓣 Cookie 探测（auth / setupGate 豁免用）
const CookieTestPath = "/admin/config/cookie/test"

// CookieCloudTestPath 只测 CookieCloud 连通和解密，不打豆瓣
const CookieCloudTestPath = "/admin/config/cookiecloud/test"

// LLMTestPath LLM 连通检测（草稿不写库）
const LLMTestPath = "/admin/config/llm/test"

// LLMModelsPath 拉取 OpenAI 兼容模型列表（草稿不写库）
const LLMModelsPath = "/admin/config/llm/models"

type configSection struct {
	ID     string
	Title  string
	Desc   string
	Items  []configField // 与 Blocks 同步扁平，测试/兼容用
	Blocks []configBlock
}

type configBlock struct {
	Title string
	Hint  string
	Class string // 色块：bg/border
	Group string // 子 tab：common/douban/weibo/feishu/pushplus
	Tools string // cookie / llm：检测按钮放块内，不跟保存挤
	Items []configField
}

type configField struct {
	Key          string
	Label        string
	Value        string
	Type         string // text/number/password/textarea/checkbox/select/sources
	Hint         string
	CanClear     bool     // 敏感项可显式清空
	ShowWhen     string   // 联动显隐：空=始终；cookie 为 raw/cookiecloud；llm 为 openai/other（逗号=多选）
	Group        string   // 子 tab：通知 common/feishu/pushplus；采集 common/douban/weibo
	Options      []string // sources / select 选项（存库值）
	OptionLabels []string // 与 Options 等长的展示文案；空则显示 Options 原文
	Advanced     bool     // 折进「高级配置」，默认收起
	Wide         bool     // 单独占满一行
	DayOffset    bool     // 天数偏移，支持小数；小字 tip 显示换算时间
	Readonly     bool     // 历史快照只读，控件 disabled
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
	"secret.filter.llm.api_style":        true,
	"secret.notifier.feishu.webhook":     true,
	"notifier.channels":                  true,
	"notifier.batch_size":                true,
	"notifier.retry_base_interval":       true,
	"secret.notifier.dingtalk.webhook":   true,
	"secret.notifier.dingtalk.secret":    true,
	"secret.notifier.wecom.webhook":      true,
	"secret.notifier.pushplus.token":     true,
	"secret.notifier.pushplus.topic":     true,
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
	apiStyle := "openai"
	ccPass := get(config.KeyDoubanCookieCloudPwd, env.Collector.Douban.CookiecloudPass)
	ppToken := get("secret.notifier.pushplus.token", env.Notifier.Pushplus.Token)
	llmBase := get("secret.filter.llm.base_url", env.Filter.LLM.BaseURL)
	if llmBase == "" {
		llmBase = "https://api.deepseek.com"
	}
	llmKey := get("secret.filter.llm.api_key", env.Filter.LLM.APIKey)
	llmModel := get("secret.filter.llm.model", env.Filter.LLM.Model)
	modelOpts := []string{""}
	if llmModel != "" {
		modelOpts = append(modelOpts, llmModel)
	}
	sourcesVal := get("collector.sources", strings.Join(app.Collector.Sources, ","))
	channelsVal := get("notifier.channels", strings.Join(app.Notifier.Channels, ","))
	sourceBase := func(source, group string) []configField {
		items := []configField{
			{Key: "collector.sources", Label: "启用", Value: sourcesVal, Type: "sources", Options: []string{source}, Group: group, Wide: true, Hint: "勾选纳入采集；修改后需重启"},
			{Key: "collector.interval", Label: "采集间隔(秒)", Value: get("collector.interval", strconv.Itoa(app.Collector.Interval)), Type: "number", Hint: "跑完一轮后等多久再开下一轮，默认 300（5 分钟）", Group: group},
			{Key: "collector.jitter_ratio", Label: "抖动比例", Value: get("collector.jitter_ratio", fmt.Sprintf("%g", app.Collector.JitterRatio)), Type: "text", Group: group},
		}
		if source != models.SourceDouban.String() {
			items = append(items, configField{Key: "collector.max_age_days", Label: "帖子时效(天)", Value: get("collector.max_age_days", strconv.Itoa(app.Collector.MaxAgeDays)), Type: "number", Hint: "超过此天数的帖不再采集。豆瓣不用这项，看「按发布时间筛选」。", Group: group, Wide: true})
		}
		return items
	}
	notifyBase := func(channel, group string) []configField {
		return []configField{
			{Key: "notifier.channels", Label: "启用", Value: channelsVal, Type: "sources", Options: []string{channel}, Group: group, Wide: true, Hint: "勾选后该渠道才会发通知；修改后需重启"},
			{Key: "notifier.batch_size", Label: "组批大小", Value: get("notifier.batch_size", strconv.Itoa(app.Notifier.BatchSize)), Type: "number", Hint: "凑满这个条数就发；没凑满则等到「重试间隔」也发。两个条件满足其一即执行发送。修改后需重启", Group: group},
			{Key: "notifier.max_attempts", Label: "最大重试", Value: get("notifier.max_attempts", strconv.Itoa(app.Notifier.MaxAttempts)), Type: "number", Group: group},
			{Key: "notifier.retry_base_interval", Label: "重试间隔(秒)", Value: get("notifier.retry_base_interval", strconv.Itoa(app.Notifier.RetryBaseInterval)), Type: "number", Group: group, Wide: true, Hint: "没凑满组批大小时，最多等这么久也发；和组批大小满足其一即执行发送。失败帖也按这个间隔再扫。修改后需重启"},
		}
	}
	return []configSection{
		makeSection("general", "常规", "服务运行基础参数", []configBlock{
			{
				Title: "服务", Hint: "进程监听", Class: "bg-slate-50 border-slate-200",
				Items: []configField{
					{Key: "server.addr", Label: "监听地址", Value: get("server.addr", app.Server.Addr), Type: "text", Hint: "如 :7777；修改后需重启"},
				},
			},
			{
				Title: "日志", Hint: "级别、落盘、控制台滚动缓冲", Class: "bg-indigo-50/70 border-indigo-100",
				Items: []configField{
					{Key: "log.level", Label: "日志级别", Value: strings.ToLower(get("log.level", app.Log.Level)), Type: "select", Options: []string{"debug", "info", "warn", "error"}, OptionLabels: []string{"DEBUG", "INFO", "WARN", "ERROR"}, Hint: "低于此级的日志不输出；改完立即生效"},
					{Key: "log.path", Label: "日志文件", Value: get("log.path", app.Log.Path), Type: "text", Hint: "留空=stdout；修改后需重启"},
					{Key: "log.memory_lines", Label: "内存日志条数", Value: get("log.memory_lines", strconv.Itoa(app.Log.MemoryLines)), Type: "number", Wide: true, Hint: "控制台「日志」页在内存里保留的条数，默认 1000。探测类 raw 单条可到数 KB，1000 条约 1–8MB；调大更占内存，调小历史翻得少。范围 100–10000，保存后立即生效。"},
				},
			},
		}),
		makeSection("collector", "采集", "按源切换；各源独立配置页，保存仍写入整分区", []configBlock{
			{
				Title: "豆瓣", Class: "bg-slate-50 border-slate-200", Group: "douban",
				Items: sourceBase(models.SourceDouban.String(), "douban"),
			},
			{
				Title: "按发布时间筛选", Hint: "只抓这个时刻之后发布的帖，截止日期永远是现在（不单独配置，也不参与采集进度指纹）。单位是天，相对现在：-10 = 10 天前。支持小数。改这个值会重置该源采集进度。", Class: "bg-sky-50 border-sky-200", Group: "douban",
				Items: []configField{
					{Key: "collector.douban.range_from", Label: "起始（几天前）", Value: config.CanonicalDayOffset(get("collector.douban.range_from", app.Collector.Douban.RangeFrom)), Type: "text", Group: "douban", DayOffset: true, Wide: true, Hint: "只采集这个时刻之后发布的帖。必须为负数，例如 -10；改了会重置采集进度"},
				},
			},
			{
				Title: "豆瓣小组与请求节奏", Hint: "抓哪些小组；同一轮里两次访问豆瓣停几秒，用来降风控。", Class: "bg-emerald-50/80 border-emerald-200", Group: "douban",
				Items: []configField{
					{Key: "collector.douban.groups", Label: "豆瓣小组 URL", Value: get("collector.douban.groups", strings.Join(app.Collector.Douban.Groups, "\n")), Type: "textarea", Hint: "每行一个；修改后需重启", Group: "douban"},
					{Key: "collector.douban.interval", Label: "请求间隔(秒)", Value: get("collector.douban.interval", strconv.Itoa(app.Collector.Douban.Interval)), Type: "number", Group: "douban", Wide: true, Hint: "同一轮里两次访问豆瓣停几秒，默认 3，用来降风控。不是上面的采集间隔。"},
				},
			},
			{
				Title: "豆瓣 Cookie", Hint: "采集访问豆瓣用的登录态。raw 自己贴；cookiecloud 从云端同步到本地后再用。", Class: "bg-amber-50 border-amber-200", Group: "douban", Tools: "cookie",
				Items: []configField{
					{Key: config.KeyDoubanCookieMode, Label: "Cookie 模式", Value: get(config.KeyDoubanCookieMode, env.Collector.Douban.CookieMode), Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 从 CookieCloud 同步", Group: "douban"},
					{Key: config.KeyDoubanCookieRaw, Label: "Cookie 原文", Value: "", Type: "textarea", CanClear: true, Hint: cookieRawHint, ShowWhen: config.CookieModeRaw.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudURL, Label: "CookieCloud 地址", Value: get(config.KeyDoubanCookieCloudURL, env.Collector.Douban.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudKey, Label: "CookieCloud UUID", Value: get(config.KeyDoubanCookieCloudKey, env.Collector.Douban.CookiecloudKey), Type: "text", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudPwd, Label: "CookieCloud 密码", Value: ccPass, Type: "text", CanClear: true, Hint: "明文回显；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban", Wide: true},
				},
			},
			{
				Title: "微博", Hint: "采集暂未实现，可先配置启用与间隔。", Class: "bg-slate-50 border-slate-200", Group: "weibo",
				Items: sourceBase(models.SourceWeibo.String(), "weibo"),
			},
		}),
		makeSection("filter", "AI", "本配置当前版本仅用于审核帖子", []configBlock{
			{
				Title: "AI 审核", Class: "bg-violet-50/80 border-violet-200", Tools: "llm",
				Items: []configField{
					{Key: "filter.ai_enabled", Label: "启用", Value: ai, Type: "checkbox", Wide: true, Hint: "关闭后跳过 AI 审核；修改后需重启"},
					{Key: "secret.filter.llm.api_style", Label: "LLM 提供方", Value: apiStyle, Type: "readonly", OptionLabels: []string{"OpenAI"}},
					{Key: "secret.filter.llm.base_url", Label: "Base URL", Value: llmBase, Type: "text", CanClear: true, Hint: "默认 https://api.deepseek.com；修改后需重启"},
					{Key: "secret.filter.llm.api_key", Label: "API Key", Value: llmKey, Type: "text", CanClear: true, Hint: "修改后需重启"},
					{Key: "secret.filter.llm.model", Label: "主模型", Value: llmModel, Type: "model_select", Options: modelOpts, Wide: true, Hint: "先填 Base URL 与 Key，再拉取列表"},
				},
			},
			{
				Title: "高级", Hint: "一般不用改", Class: "bg-slate-50 border-dashed border-slate-200",
				Items: []configField{
					{Key: "filter.batch_size", Label: "组批大小", Value: get("filter.batch_size", strconv.Itoa(app.Filter.BatchSize)), Type: "number", Hint: "筛选一次抽干上限，有帖立刻处理不等凑批", Advanced: true},
					{Key: "filter.ai_batch_size", Label: "AI 批大小", Value: get("filter.ai_batch_size", strconv.Itoa(app.Filter.AIBatchSize)), Type: "number", Hint: "AI 审核从库读 pending 凑满此数（或超时）再调模型", Advanced: true},
				},
			},
		}),
		makeSection("notifier", "通知", "按渠道切换；各渠道独立配置页，保存仍写入整分区", []configBlock{
			{
				Title: "飞书", Hint: "发送节奏与 Webhook", Class: "bg-sky-50 border-sky-200", Group: "feishu",
				Items: append(notifyBase("feishu", "feishu"), configField{
					Key: "secret.notifier.feishu.webhook", Label: "飞书 Webhook", Value: "", Type: "password", CanClear: true, Hint: "修改后需重启服务", Group: "feishu", Wide: true,
				}),
			},
			{
				Title: "PushPlus", Hint: "发送节奏与 Token；一对多填群组编码，空则一对一", Class: "bg-orange-50 border-orange-200", Group: "pushplus",
				Items: append(notifyBase("pushplus", "pushplus"),
					configField{
						Key: "secret.notifier.pushplus.token", Label: "PushPlus Token", Value: ppToken, Type: "text", CanClear: true, Hint: "明文回显；修改后需重启服务", Group: "pushplus", Wide: true,
					},
					configField{
						Key: "secret.notifier.pushplus.topic", Label: "群组编码", Value: get("secret.notifier.pushplus.topic", env.Notifier.Pushplus.Topic), Type: "text", CanClear: true, Hint: "一对多 topic，如 doubanzufang；留空走一对一。修改后需重启", Group: "pushplus", Wide: true,
					},
				),
			},
		}),
		makeSection("admin", "管理", "控制台鉴权", []configBlock{
			{
				Title: "鉴权", Class: "bg-rose-50/70 border-rose-100",
				Items: []configField{
					{Key: "admin.auth_required", Label: "启用鉴权", Value: get("admin.auth_required", auth), Type: "checkbox"},
					{Key: "admin.token", Label: "访问 Token", Value: "", Type: "password", CanClear: true},
				},
			},
		}),
	}
}

func makeSection(id, title, desc string, blocks []configBlock) configSection {
	var items []configField
	for _, b := range blocks {
		items = append(items, b.Items...)
	}
	return configSection{ID: id, Title: title, Desc: desc, Items: items, Blocks: blocks}
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
		if key == "collector.sources" {
			v = joinFormValues(values)
		}
		v = config.NormalizeValue(v)
		if key == "collector.douban.range_from" {
			v = config.CanonicalDayOffset(v)
			if v == "" {
				v = "-10"
			}
		}
		if key == "secret.filter.llm.api_style" {
			v = "openai"
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
	if allowed["notifier.channels"] {
		updates["notifier.channels"] = joinFormValues(form["notifier.channels"])
	}
	if allowed["secret.filter.llm.api_style"] {
		updates["secret.filter.llm.api_style"] = "openai"
	}
	if allowed["filter.ai_enabled"] {
		if form.Get("filter.ai_enabled") == "on" {
			updates["filter.ai_enabled"] = "true"
		} else {
			updates["filter.ai_enabled"] = "false"
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
	return config.ParseLLMAPIStyle(s).String()
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

// snapshotSections 历史快照：用当时 KV 填表单，全部只读，敏感值打码
func snapshotSections(kv map[string]string) []configSection {
	merged := config.MergeKV(config.MergeKV(config.AppToKV(config.DefaultApp()), config.SecretsToKV(config.DefaultSecrets())), kv)
	secs := buildConfigSections(config.KVToApp(merged), config.KVToSecrets(merged), merged)
	for i := range secs {
		secs[i].Blocks = redactReadonlyBlocks(secs[i].Blocks)
		secs[i].Items = nil
		for _, b := range secs[i].Blocks {
			secs[i].Items = append(secs[i].Items, b.Items...)
		}
	}
	return secs
}

func redactReadonlyBlocks(blocks []configBlock) []configBlock {
	out := make([]configBlock, len(blocks))
	for i, b := range blocks {
		b.Tools = ""
		items := make([]configField, len(b.Items))
		for j, f := range b.Items {
			f.Readonly = true
			f.CanClear = false
			if secretInput(f) && strings.TrimSpace(f.Value) != "" {
				f.Value = "••••••••"
			}
			items[j] = f
		}
		b.Items = items
		out[i] = b
	}
	return out
}

func secretInput(f configField) bool {
	if !strings.HasPrefix(f.Key, "secret.") {
		return false
	}
	return f.Type == "password" || f.Type == "textarea" || f.Type == "text"
}
