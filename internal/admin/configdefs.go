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

// NotifyTestPath 飞书/PushPlus 草稿试发（不写通知账本）
const NotifyTestPath = "/admin/config/notify/test"

const aiSectionDesc = `本配置当前版本仅用于审核帖子：大模型不参与采集，也不改帖子主状态（collected / passed / rejected）。硬规则（白名单地点、黑名单词）先筛一遍，通过的帖再交给 AI 打徽章，并尽量补全月租、联系方式、通勤描述。

系统提示词固定为「租房信息筛选助手」。规则页里启用的自然语言规则整批写入 system，和判定标准共用一次：满足任一条即通过；只依据帖文、不做无依据推测；不确定时倾向通过。同时要求抽出月租金（整数元，区间取下限，没有填 0）、微信/手机等原文、通勤/交通原文。理由限中文约 30 字。user 侧只放本批精简帖，不重复规则。

为省 token：详情 HTML 去掉标签和图片链接，正文按 500 字截断；同一批共用一份 system，多帖拼进一次请求（凑满 AI 批大小或等到超时再调）。请求带 json_schema，约束返回 {"verdicts":[...]}；temperature 0.1，不塞 few-shot 示例。

入库时标题和正文还会用正则抠价格、联系方式。模型返回了有效价格或联系方式，再写回 posts。关闭本页开关或密钥为空时，审核协程仍在，本轮直接跳过。`

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
	Tools string // cookie / llm / notify：检测按钮放块内
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
	NeedRestart  bool     // 启动时钉死，保存后要重启才吃进
}

// RestartKeys 进程启动时读进对象、运行中不再跟 HotConfig 的项。
// 采集间隔/Cookie/鉴权 token/对外地址/内存日志条数不在此列（保存后 ReloadOnce 即生效）。
var RestartKeys = map[string]bool{
	"server.addr": true, // ListenAndServe 绑死
	"log.path":    true, // pkglog.New 只跑一次
	"log.level":   true, // slog Handler 级别启动钉死
	"log.format":  true,
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
	cookieRawHint := func(ck config.DoubanCookieConfig) string {
		if raw := ck.CookieRaw; raw != "" {
			return fmt.Sprintf("已保存 · 长度 %d；留空不修改", len(raw))
		}
		return "粘贴 cookie 原文；留空不修改"
	}
	apiStyle := "openai"
	ccPass := get(config.KeyDoubanCookieCloudPwd, env.Collector.Douban.CookiecloudPass)
	weiboCCPass := get(config.KeyWeiboCookieCloudPwd, env.Collector.Weibo.CookiecloudPass)
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
			{Key: "collector.sources", Label: "启用", Value: sourcesVal, Type: "sources", Options: []string{source}, Group: group, Wide: true, Hint: "勾选纳入采集"},
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
			{Key: "notifier.channels", Label: "启用", Value: channelsVal, Type: "sources", Options: []string{channel}, Group: group, Wide: true, Hint: "勾选后该渠道才会发通知"},
			{Key: "notifier.batch_size", Label: "组批大小", Value: get("notifier.batch_size", strconv.Itoa(app.Notifier.BatchSize)), Type: "number", Hint: "凑满这个条数就发；没凑满则等到「重试间隔」也发。两个条件满足其一即执行发送", Group: group},
			{Key: "notifier.max_attempts", Label: "最大重试", Value: get("notifier.max_attempts", strconv.Itoa(app.Notifier.MaxAttempts)), Type: "number", Group: group},
			{Key: "notifier.retry_base_interval", Label: "重试间隔(秒)", Value: get("notifier.retry_base_interval", strconv.Itoa(app.Notifier.RetryBaseInterval)), Type: "number", Group: group, Wide: true, Hint: "没凑满组批大小时，最多等这么久也发；和组批大小满足其一即执行发送。失败帖也按这个间隔再扫"},
		}
	}
	return []configSection{
		makeSection("general", "常规", "服务运行基础参数", []configBlock{
			{
				Title: "服务", Hint: "进程监听", Class: "bg-slate-50 border-slate-200",
				Items: []configField{
					{Key: "server.addr", Label: "监听地址", Value: get("server.addr", app.Server.Addr), Type: "text", Hint: "进程绑定，默认 :7777（所有网卡）。不要填局域网 IP，否则本机 127.0.0.1 会连不上"},
					{Key: "server.public_base", Label: "对外访问地址", Value: get("server.public_base", app.Server.PublicBase), Type: "text", Wide: true, Hint: "通知里「有用/无用/已处理」三条链接的前缀。留空则发送时自动用本机局域网 IPv4 + 监听端口，例如 http://192.168.1.8:7777。手机点开卡片需要这个"},
				},
			},
			{
				Title: "日志", Hint: "级别、落盘、控制台滚动缓冲", Class: "bg-indigo-50/70 border-indigo-100",
				Items: []configField{
					{Key: "log.level", Label: "日志级别", Value: strings.ToLower(get("log.level", app.Log.Level)), Type: "select", Options: []string{"debug", "info", "warn", "error"}, OptionLabels: []string{"DEBUG", "INFO", "WARN", "ERROR"}, Hint: "低于此级的日志不输出"},
					{Key: "log.path", Label: "日志文件", Value: get("log.path", app.Log.Path), Type: "text", Hint: "留空=stdout"},
					{Key: "log.memory_lines", Label: "内存日志条数", Value: get("log.memory_lines", strconv.Itoa(app.Log.MemoryLines)), Type: "number", Wide: true, Hint: "控制台「日志」页在内存里保留的条数，默认 1000。探测类 raw 单条可到数 KB，1000 条约 1–8MB；调大更占内存，调小历史翻得少。范围 100–10000"},
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
					{Key: "collector.douban.groups", Label: "豆瓣小组 URL", Value: get("collector.douban.groups", strings.Join(app.Collector.Douban.Groups, "\n")), Type: "textarea", Hint: "每行一个网址。单独一行的 #话题 或非 http 行当注释，保存后仍保留。", Group: "douban"},
					{Key: "collector.douban.interval", Label: "请求间隔(秒)", Value: get("collector.douban.interval", strconv.Itoa(app.Collector.Douban.Interval)), Type: "number", Group: "douban", Wide: true, Hint: "同一轮里两次访问豆瓣停几秒，默认 3，用来降风控。不是上面的采集间隔。"},
				},
			},
			{
				Title: "豆瓣 Cookie", Hint: "采集访问豆瓣用的登录态。raw 自己贴；cookiecloud 从云端同步到本地后再用。", Class: "bg-amber-50 border-amber-200", Group: "douban", Tools: "cookie",
				Items: []configField{
					{Key: config.KeyDoubanCookieMode, Label: "Cookie 模式", Value: get(config.KeyDoubanCookieMode, env.Collector.Douban.CookieMode), Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 从 CookieCloud 同步", Group: "douban"},
					{Key: config.KeyDoubanCookieRaw, Label: "Cookie 原文", Value: "", Type: "textarea", CanClear: true, Hint: cookieRawHint(env.Collector.Douban), ShowWhen: config.CookieModeRaw.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudURL, Label: "CookieCloud 地址", Value: get(config.KeyDoubanCookieCloudURL, env.Collector.Douban.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudKey, Label: "CookieCloud UUID", Value: get(config.KeyDoubanCookieCloudKey, env.Collector.Douban.CookiecloudKey), Type: "text", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudPwd, Label: "CookieCloud 密码", Value: ccPass, Type: "text", CanClear: true, Hint: "明文回显；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban", Wide: true},
				},
			},
			{
				Title: "微博", Hint: "采集暂未实现，可先配超话地址与启用。", Class: "bg-slate-50 border-slate-200", Group: "weibo",
				Items: sourceBase(models.SourceWeibo.String(), "weibo"),
			},
			{
				Title: "微博超话", Hint: "搜索/超话页地址，采集接上后按这些 URL 拉帖。", Class: "bg-emerald-50/80 border-emerald-200", Group: "weibo",
				Items: []configField{
					{Key: "collector.weibo.urls", Label: "微博超话 URL", Value: get("collector.weibo.urls", strings.Join(app.Collector.Weibo.URLs, "\n")), Type: "textarea", Hint: "每行一个网址。#话题名 单独一行当注释，采集只认 http(s)。", Group: "weibo"},
				},
			},
			{
				Title: "微博 Cookie", Hint: "采集访问微博用的登录态。与豆瓣同一套模式；CookieCloud 只拼 weibo.com。账号可与豆瓣相同也可分开填。", Class: "bg-amber-50 border-amber-200", Group: "weibo", Tools: "cookie",
				Items: []configField{
					{Key: config.KeyWeiboCookieMode, Label: "Cookie 模式", Value: get(config.KeyWeiboCookieMode, env.Collector.Weibo.CookieMode), Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 从 CookieCloud 同步", Group: "weibo"},
					{Key: config.KeyWeiboCookieRaw, Label: "Cookie 原文", Value: "", Type: "textarea", CanClear: true, Hint: cookieRawHint(env.Collector.Weibo), ShowWhen: config.CookieModeRaw.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudURL, Label: "CookieCloud 地址", Value: get(config.KeyWeiboCookieCloudURL, env.Collector.Weibo.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudKey, Label: "CookieCloud UUID", Value: get(config.KeyWeiboCookieCloudKey, env.Collector.Weibo.CookiecloudKey), Type: "text", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudPwd, Label: "CookieCloud 密码", Value: weiboCCPass, Type: "text", CanClear: true, Hint: "明文回显；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo", Wide: true},
				},
			},
		}),
		makeSection("filter", "AI", aiSectionDesc, []configBlock{
			{
				Title: "AI 审核", Class: "bg-violet-50/80 border-violet-200", Tools: "llm",
				Items: []configField{
					{Key: "filter.ai_enabled", Label: "启用", Value: ai, Type: "checkbox", Wide: true, Hint: "关闭后跳过 AI 审核"},
					{Key: "secret.filter.llm.api_style", Label: "LLM 提供方", Value: apiStyle, Type: "readonly", OptionLabels: []string{"OpenAI"}},
					{Key: "secret.filter.llm.base_url", Label: "Base URL", Value: llmBase, Type: "text", CanClear: true, Hint: "默认 https://api.deepseek.com"},
					{Key: "secret.filter.llm.api_key", Label: "API Key", Value: llmKey, Type: "text", CanClear: true},
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
				Title: "飞书", Hint: "发送节奏与 Webhook", Class: "bg-sky-50 border-sky-200", Group: "feishu", Tools: "notify",
				Items: append(notifyBase("feishu", "feishu"), configField{
					Key: "secret.notifier.feishu.webhook", Label: "飞书 Webhook", Value: "", Type: "password", CanClear: true, Group: "feishu", Wide: true,
				}),
			},
			{
				Title: "PushPlus", Hint: "发送节奏与 Token；一对多填群组编码，空则一对一", Class: "bg-orange-50 border-orange-200", Group: "pushplus", Tools: "notify",
				Items: append(notifyBase("pushplus", "pushplus"),
					configField{
						Key: "secret.notifier.pushplus.token", Label: "PushPlus Token", Value: ppToken, Type: "text", CanClear: true, Hint: "明文回显", Group: "pushplus", Wide: true,
					},
					configField{
						Key: "secret.notifier.pushplus.topic", Label: "群组编码", Value: get("secret.notifier.pushplus.topic", env.Notifier.Pushplus.Topic), Type: "text", CanClear: true, Hint: "一对多 topic，如 doubanzufang；留空走一对一", Group: "pushplus", Wide: true,
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
	for i := range blocks {
		for j := range blocks[i].Items {
			if RestartKeys[blocks[i].Items[j].Key] {
				blocks[i].Items[j].NeedRestart = true
			}
			items = append(items, blocks[i].Items[j])
		}
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
