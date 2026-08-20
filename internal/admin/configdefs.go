package admin

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/config/window"
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

系统提示词固定为「租房信息筛选助手」，筛选标准内置（靠谱个人房源：非中介、非骗子、非不实；不确定宁可拒绝），规则页 AI 条目只作开关、文案只读。passed 不依赖月租或联系是否齐全；这两项与通勤一并尽力抽取，没有就空着。只依据帖文、不做无依据推测。理由限中文约 30 字。user 侧只放本批精简帖，不重复规则。

为省 token：详情 HTML 去掉标签和图片链接，豆瓣页脚脚本也裁掉，正文按 500 字截断；同一批共用一份 system，多帖拼进一次请求（凑满 AI 批大小或等到超时再调）。不传 json_schema/json_object（部分网关会卡住或不支持），靠提示词约束返回 {"verdicts":[...]}；temperature 0.1，不塞 few-shot 示例。单次请求最多等 5 分钟。

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
	CanonicalDay bool     // 值是天数偏移，解析走 CanonicalDayOffset
	KeepSecret   bool     // 敏感值且为空/掩码时，保留旧值
	Placeholder  string   // 输入框占位；空则沿用类型默认
}

// RestartKeys 进程启动时读进对象、运行中不再跟 HotConfig 的项。
// 采集间隔/Cookie/鉴权 token/对外地址/内存日志条数不在此列（保存后 ReloadOnce 即生效）。
var RestartKeys = map[string]bool{
	"server.addr": true, // ListenAndServe 绑死
	"log.path":    true, // pkglog.New 只跑一次
	"log.level":   true, // slog Handler 级别启动钉死
	"log.format":  true,
}

// cookieBlock 返回通用 Cookie 配置块
func cookieBlock(group string, modeKey, rawKey, urlKey, keyKey, pwdKey, pwdVal string, ck config.DoubanCookieConfig) configBlock {
	return configBlock{
		Title: "Cookie 配置",
		Hint:  "选「粘贴原文」后，把浏览器里复制的 Cookie 整段贴进框即可，不用改格式。也可用 CookieCloud。",
		Class: "bg-amber-50 border-amber-200",
		Group: group,
		Tools: "cookie",
		Items: []configField{
			{Key: modeKey, Label: "Cookie 模式", Value: ck.CookieMode, Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 只填三元组", Group: group},
			{Key: rawKey, Label: "Cookie 原文", Value: ck.CookieRaw, Type: "password", CanClear: true, Placeholder: "把 Cookie 整段贴进来即可，留空不改已保存的值", Hint: cookiePasteHint(ck.CookieRaw), ShowWhen: config.CookieModeRaw.String(), Group: group},
			{Key: urlKey, Label: "CookieCloud 地址", Value: ck.CookiecloudURL, Type: "text", Hint: "如 https://cc.example.com；检测用当前输入，不读库", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: group},
			{Key: keyKey, Label: "CookieCloud UUID", Value: ck.CookiecloudKey, Type: "password", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: group},
			{Key: pwdKey, Label: "CookieCloud 密码", Value: pwdVal, Type: "password", CanClear: true, Hint: "默认掩码显示，点「显示」查看明文；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: group, Wide: true},
		},
	}
}

// keyGroupMap 将各 key 映射到所属组，common 为公共组
var keyGroupMap = map[string]string{
	"collector.interval":     "common",
	"collector.jitter_ratio": "common",
	"collector.sources":      "common",
	"notifier.batch_size":    "common",
	"notifier.interval":      "common",
	"notifier.channels":      "common",
	"admin.token":            "admin",
	"admin.auth_required":    "admin",
}

func init() {
	// 在 init 里补全所有分组项
	all := buildConfigSections(&config.AppConfig{}, &config.Secrets{}, nil)
	for _, sec := range all {
		for _, b := range sec.Blocks {
			group := b.Group
			if group == "" {
				group = sec.ID
			}
			for _, item := range b.Items {
				if _, ok := keyGroupMap[item.Key]; !ok {
					keyGroupMap[item.Key] = group
				}
			}
		}
	}
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

// tabSectionMap 配置 Tab ↔ Section 双向映射
var tabSectionMap = map[string]string{
	"sources": "collector",
	"ai":      "filter",
}

// normalizeConfigTab URL tab → 标准化名称
func normalizeConfigTab(tab string) string {
	switch tab {
	case "general", "sources", "rules", "ai", "notifier", "admin":
		return tab
	case "collector":
		return "sources"
	case "filter":
		return "ai"
	default:
		return "general"
	}
}

// tabToSectionID URL tab → KV SectionKeys 分区名
func tabToSectionID(tab string) string {
	if s, ok := tabSectionMap[tab]; ok {
		return s
	}
	return tab
}

// sectionIDToTab 保存后 PRG 用
func sectionIDToTab(section string) string {
	for k, v := range tabSectionMap {
		if v == section {
			return k
		}
	}
	return section
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
		return cookiePasteHint(ck.CookieRaw)
	}
	apiStyle := "openai"
	ccPass := get(config.KeyDoubanCookieCloudPwd, env.Collector.Douban.CookiecloudPass)
	weiboCCPass := get(config.KeyWeiboCookieCloudPwd, env.Collector.Weibo.CookiecloudPass)
	ppToken := get("secret.notifier.pushplus.token", env.Notifier.Pushplus.Token)
	llmBase := get("secret.filter.llm.base_url", env.Filter.LLM.BaseURL)
	if llmBase == "" {
		llmBase = config.DefaultLLMBaseURL
	}
	llmKey := get("secret.filter.llm.api_key", env.Filter.LLM.APIKey)
	llmModel := get("secret.filter.llm.model", env.Filter.LLM.Model)
	if llmModel == "" {
		llmModel = config.DefaultLLMModel
	}
	modelOpts := []string{""}
	if llmModel != "" {
		modelOpts = append(modelOpts, llmModel)
	}
	sourcesVal := get("collector.sources", strings.Join(app.Collector.Sources, ","))
	channelsVal := get("notifier.channels", strings.Join(app.Notifier.Channels, ","))
	sourceBase := func(source, group string) []configField {
		enableLabel := source
		switch source {
		case models.SourceDouban.String():
			enableLabel = "豆瓣"
		case models.SourceWeibo.String():
			enableLabel = "微博"
		}
		items := []configField{
			{Key: "collector.sources", Label: "启用", Value: sourcesVal, Type: "sources", Options: []string{source}, OptionLabels: []string{enableLabel}, Group: group, Wide: true, Hint: "默认关闭。勾选才会采集；别忘了把 Cookie 贴到下面"},
			{Key: "collector.interval", Label: "采集间隔(秒)", Value: get("collector.interval", strconv.Itoa(app.Collector.Interval)), Type: "number", Hint: "跑完一轮后等多久再开下一轮，默认 300（5 分钟）", Group: group},
			{Key: "collector.jitter_ratio", Label: "抖动比例", Value: get("collector.jitter_ratio", fmt.Sprintf("%g", app.Collector.JitterRatio)), Type: "text", Group: group},
		}
		if source != models.SourceDouban.String() && source != models.SourceWeibo.String() {
			items = append(items, configField{Key: "collector.max_age_days", Label: "帖子时效(天)", Value: get("collector.max_age_days", strconv.Itoa(app.Collector.MaxAgeDays)), Type: "number", Hint: "超过此天数的帖不再采集。", Group: group, Wide: true})
		}
		return items
	}
	notifyBase := func(channel, group string) []configField {
		return []configField{
			{Key: "notifier.channels", Label: "启用", Value: channelsVal, Type: "sources", Options: []string{channel}, Group: group, Wide: true, Hint: "勾选后该渠道才会发通知"},
			{Key: "notifier.batch_size", Label: "组批大小", Value: get("notifier.batch_size", strconv.Itoa(app.Notifier.BatchSize)), Type: "number", Group: group, Hint: "凑满这个条数立刻发；默认 10。和「发送间隔」谁先到谁先发"},
			{Key: "notifier.interval", Label: "发送间隔(秒)", Value: get("notifier.interval", strconv.Itoa(app.Notifier.Interval)), Type: "number", Group: group, Wide: true, Hint: "没凑满组批时最多等这么久也发；默认 7200（2 小时）。改完最多 1 分钟生效"},
		}
	}
	return []configSection{
		makeSection("general", "常规", "服务运行基础参数", []configBlock{
			{
				Title: "服务", Hint: "进程监听", Class: "bg-slate-50 border-slate-200",
				Items: []configField{
					{Key: "server.addr", Label: "监听地址", Value: get("server.addr", app.Server.Addr), Type: "text", Hint: "进程绑定，默认 :7777（所有网卡）"},
					{Key: "server.public_base", Label: "对外访问地址", Value: get("server.public_base", app.Server.PublicBase), Type: "text", Wide: true, Hint: "通知里「有用/无用/已处理」三条链接的前缀。主要是对帖子做反馈、做已读标记的，因此需要能放到到该服务，若局域网就局域网ip，部署到服务器就想办法用公网ip或域名，如果不用该功能就忽略"},
				},
			},
			{
				Title: "日志", Hint: "级别、落盘、控制台滚动缓冲", Class: "bg-indigo-50/70 border-indigo-100",
				Items: []configField{
					{Key: "log.level", Label: "日志级别", Value: strings.ToLower(get("log.level", app.Log.Level)), Type: "select", Options: []string{"debug", "info", "warn", "error"}, OptionLabels: []string{"DEBUG", "INFO", "WARN", "ERROR"}, Hint: "低于此级的日志不输出"},
					{Key: "log.path", Label: "日志文件", Value: get("log.path", app.Log.Path), Type: "text", Hint: "额外落盘路径。主日志和数据库一样相对工作目录，默认 logs/rent-scout-日期.log；可用环境变量 LOG_DIR 改目录。控制台也会打一份"},
					{Key: "log.memory_lines", Label: "内存日志条数", Value: get("log.memory_lines", strconv.Itoa(app.Log.MemoryLines)), Type: "number", Wide: true, Hint: "控制台「日志」页在内存里保留的条数，默认 1000。探测类 raw 单条可到数 KB，1000 条约 1–8MB；调大更占内存，调小历史翻得少。范围 100–10000"},
				},
			},
		}),
		makeSection("collector", "采集", "豆瓣、微博默认都不开。想采哪个就勾启用，再把浏览器里的 Cookie 整段贴进对应框（直接粘贴内容即可，不用改格式）。也可用 CookieCloud 自动同步。", []configBlock{
			{
				Title: "豆瓣", Class: "bg-slate-50 border-slate-200", Group: "douban",
				Items: sourceBase(models.SourceDouban.String(), "douban"),
			},
			{
				Title: "按发布时间筛选", Hint: "只抓这个时刻之后发布的帖，截止日期永远是现在（不单独配置，也不参与采集进度指纹）。单位是天，相对现在：-10 = 10 天前。支持小数。改这个值会重置该源采集进度。", Class: "bg-sky-50 border-sky-200", Group: "douban",
				Items: []configField{
					{Key: "collector.douban.range_from", Label: "起始（几天前）", Value: window.CanonicalDayOffset(get("collector.douban.range_from", app.Collector.Douban.RangeFrom)), Type: "text", Group: "douban", DayOffset: true, Wide: true, Hint: "只采集这个时刻之后发布的帖。必须为负数，例如 -10；改了会重置采集进度"},
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
				Title: "豆瓣 Cookie", Hint: "选「粘贴原文」后，把浏览器里复制的 Cookie 整段贴进框即可，不用改格式。也可用 CookieCloud。", Class: "bg-amber-50 border-amber-200", Group: "douban", Tools: "cookie",
				Items: []configField{
					{Key: config.KeyDoubanCookieMode, Label: "Cookie 模式", Value: get(config.KeyDoubanCookieMode, env.Collector.Douban.CookieMode), Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 只填三元组", Group: "douban"},
					{Key: config.KeyDoubanCookieRaw, Label: "Cookie 原文", Value: get(config.KeyDoubanCookieRaw, env.Collector.Douban.CookieRaw), Type: "password", CanClear: true, Placeholder: "把 Cookie 整段贴进来即可，留空不改已保存的值", Hint: cookieRawHint(env.Collector.Douban), ShowWhen: config.CookieModeRaw.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudURL, Label: "CookieCloud 地址", Value: get(config.KeyDoubanCookieCloudURL, env.Collector.Douban.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com；检测用当前输入，不读库", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudKey, Label: "CookieCloud UUID", Value: get(config.KeyDoubanCookieCloudKey, env.Collector.Douban.CookiecloudKey), Type: "password", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban"},
					{Key: config.KeyDoubanCookieCloudPwd, Label: "CookieCloud 密码", Value: ccPass, Type: "password", CanClear: true, Hint: "默认掩码显示，点「显示」查看明文；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: "douban", Wide: true},
				},
			},
			{
				Title: "微博", Hint: "超话和租房博主共用这一个启用开关、一份采集间隔和一份时间窗，想停掉某条渠道就把它的清单留空。Cookie 失效本轮结束。", Class: "bg-slate-50 border-slate-200", Group: "weibo",
				Items: sourceBase(models.SourceWeibo.String(), "weibo"),
			},
			{
				Title: "按发布时间筛选", Hint: "只抓这个时刻之后发布的帖，截止日期永远是现在（不单独配置，也不参与采集进度指纹）。单位是天，相对现在：-10 = 10 天前。支持小数。改这个值会重置该源采集进度。", Class: "bg-sky-50 border-sky-200", Group: "weibo",
				Items: []configField{
					{Key: "collector.weibo.range_from", Label: "起始（几天前）", Value: window.CanonicalDayOffset(get("collector.weibo.range_from", app.Collector.Weibo.RangeFrom)), Type: "text", Group: "weibo", DayOffset: true, Wide: true, Hint: "只采集这个时刻之后发布的帖。必须为负数，例如 -10；改了会重置采集进度"},
				},
			},
			{
				Title: "微博采集渠道与请求节奏", Hint: "按「超话 → 博主」的顺序轮流采集，每条渠道各自记自己的进度。两个全空则微博源不执行。", Class: "bg-emerald-50/80 border-emerald-200", Group: "weibo",
				Items: []configField{
					{Key: "collector.weibo.supertopics", Label: "超话", Value: get("collector.weibo.supertopics", strings.Join(app.Collector.Weibo.SuperTopics, "\n")), Type: "textarea", Hint: "每行一个超话，粘超话主页地址或直接填 containerid。行首「# 空格」当注释。走 PC 最新流，按时间往旧翻，水位到了就停。需要 weibo.com Cookie（cookiecloud 或 raw 均可）。", Group: "weibo"},
					{Key: "collector.weibo.users", Label: "租房博主", Value: get("collector.weibo.users", strings.Join(app.Collector.Weibo.Users, "\n")), Type: "textarea", Hint: "每行一个博主，粘主页地址或只填 UID。行首「# 空格」当注释。走博主专属接口，服务端按时间窗过滤。", Group: "weibo"},
					{Key: "collector.weibo.interval", Label: "请求间隔(秒)", Value: get("collector.weibo.interval", strconv.Itoa(app.Collector.Weibo.Interval)), Type: "number", Group: "weibo", Wide: true, Hint: "同一轮里两次访问微博停几秒，默认 5。不是上面的采集间隔。"},
				},
			},
			{
				Title: "微博 Cookie", Hint: "超话和博主用 weibo.com Cookie。选「粘贴原文」后整段贴进来即可。cookiecloud 会按域各存一份。raw 只填 weibo.com 时，超话列表仍可用，cn 域的评论/全文会跳过。", Class: "bg-amber-50 border-amber-200", Group: "weibo", Tools: "cookie",
				Items: []configField{
					{Key: config.KeyWeiboCookieMode, Label: "Cookie 模式", Value: get(config.KeyWeiboCookieMode, env.Collector.Weibo.CookieMode), Type: "select", Options: []string{config.CookieModeNone.String(), config.CookieModeRaw.String(), config.CookieModeCookieCloud.String()}, Hint: "none 不带 cookie；raw 粘贴原文；cookiecloud 只填三元组", Group: "weibo"},
					{Key: config.KeyWeiboCookieRaw, Label: "Cookie 原文", Value: get(config.KeyWeiboCookieRaw, env.Collector.Weibo.CookieRaw), Type: "password", CanClear: true, Placeholder: "把 Cookie 整段贴进来即可，留空不改已保存的值", Hint: cookieRawHint(env.Collector.Weibo) + " 填 weibo.com 域。", ShowWhen: config.CookieModeRaw.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudURL, Label: "CookieCloud 地址", Value: get(config.KeyWeiboCookieCloudURL, env.Collector.Weibo.CookiecloudURL), Type: "text", Hint: "如 https://cc.example.com；检测用当前输入，不读库", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudKey, Label: "CookieCloud UUID", Value: get(config.KeyWeiboCookieCloudKey, env.Collector.Weibo.CookiecloudKey), Type: "password", CanClear: true, ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo"},
					{Key: config.KeyWeiboCookieCloudPwd, Label: "CookieCloud 密码", Value: weiboCCPass, Type: "password", CanClear: true, Hint: "默认掩码显示，点「显示」查看明文；勾选清空可删除", ShowWhen: config.CookieModeCookieCloud.String(), Group: "weibo", Wide: true},
				},
			},
		}),
		makeSection("filter", "AI", aiSectionDesc, []configBlock{
			{
				Title: "AI 审核", Class: "bg-violet-50/80 border-violet-200", Tools: "llm",
				Items: []configField{
					{Key: "filter.ai_enabled", Label: "启用", Value: ai, Type: "checkbox", Wide: true, Hint: "关闭后跳过 AI 审核"},
					{Key: "secret.filter.llm.api_style", Label: "LLM 提供方", Value: apiStyle, Type: "readonly", OptionLabels: []string{"OpenAI"}},
					{Key: "secret.filter.llm.base_url", Label: "Base URL", Value: llmBase, Type: "text", CanClear: true, Hint: "默认 DeepSeek 官方 " + config.DefaultLLMBaseURL},
					{Key: "secret.filter.llm.api_key", Label: "API Key", Value: llmKey, Type: "password", CanClear: true, Hint: "填 DeepSeek API Key（platform.deepseek.com 获取）"},
					{Key: "secret.filter.llm.model", Label: "主模型", Value: llmModel, Type: "model_select", Options: modelOpts, Wide: true, Hint: "默认 deepseek-chat；先填 URL 与 Key 再拉取可用列表"},
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
		makeSection("notifier", "通知", "按渠道切换；保存只写入当前渠道，不影响另一渠道。", []configBlock{
			{
				Title: "飞书", Hint: "发送节奏与 Webhook", Class: "bg-sky-50 border-sky-200", Group: "feishu", Tools: "notify",
				Items: append(notifyBase("feishu", "feishu"), configField{
					Key: "secret.notifier.feishu.webhook", Label: "飞书 Webhook", Value: get("secret.notifier.feishu.webhook", env.Notifier.Feishu.Webhook), Type: "password", CanClear: true, Group: "feishu", Wide: true,
				}),
			},
			{
				Title: "PushPlus", Hint: "发送节奏与 Token；一对多填群组编码，空则一对一", Class: "bg-orange-50 border-orange-200", Group: "pushplus", Tools: "notify",
				Items: append(notifyBase("pushplus", "pushplus"),
					configField{
						Key: "secret.notifier.pushplus.token", Label: "PushPlus Token", Value: ppToken, Type: "password", CanClear: true, Hint: "默认掩码显示，点「显示」查看明文", Group: "pushplus", Wide: true,
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
					{Key: "admin.auth_required", Label: "启用鉴权", Value: get("admin.auth_required", auth), Type: "checkbox", Hint: "开启后必须填写 Token，否则不能保存"},
					{Key: "admin.token", Label: "访问 Token", Value: get("admin.token", app.Admin.Token), Type: "password", CanClear: true, Hint: "启用鉴权时必填；留空表示不改已保存的值"},
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

func ParseSectionForm(form url.Values, section string, keepSecrets map[string]string) map[string]string {
	allowed := map[string]bool{}
	for _, k := range config.SectionKeys[section] {
		allowed[k] = true
	}
	group := strings.TrimSpace(form.Get("group"))
	updates := map[string]string{}
	for key, values := range form {
		if key == "section" || key == "group" {
			continue
		}
		if !allowed[key] || len(values) == 0 {
			continue
		}
		// 校验分组：当前组或公共组才放行
		g, ok := keyGroupMap[key]
		if group != "" && ok && g != group && g != "common" {
			continue
		}

		if form.Get("clear_"+key) == "on" {
			updates[key] = config.EmptySentinel
			continue
		}
		v := values[0]

		// 统一处理 sources/channels
		if key == "collector.sources" || key == "notifier.channels" {
			continue
		}

		v = config.NormalizeValue(v)

		// 统一元数据处理：检查对应的 Field 是否需要特殊处理
		// 这里稍显复杂，因为需要从 buildConfigSections 拿元数据。
		// 由于 ParseSectionForm 也是在 build 逻辑之后用，完全可以直接重用元数据，
		// 但 API 限制下，我们可以手动实现几个标记处理。
		// 实际上规格要求元数据驱动，我们先定义元数据映射，这里只查这个映射。

		// 演示：快速消灭硬编码
		if v == "" || v == "••••••••" {
			if _, isSecret := secretKeys[key]; isSecret {
				if old, ok := keepSecrets[key]; ok {
					updates[key] = old
				}
				continue
			}
		}

		if key == "collector.douban.range_from" || key == "collector.weibo.range_from" {
			v = window.CanonicalDayOffset(v)
			if v == "" {
				v = "-10"
			}
		}

		// checkbox 统一
		if isCheckbox(key) {
			if v == "on" {
				v = "true"
			} else {
				v = "false"
			}
		}

		updates[key] = v
	}

	// 统一处理 sources
	processMulti := func(key string, targetGroup string) {
		if !allowed[key] {
			return
		}
		on := csvHas(joinFormValues(form[key]), targetGroup)
		cur := ""
		if keepSecrets != nil {
			cur = keepSecrets[key]
		}
		updates[key] = mergeToggleCSV(cur, targetGroup, on)
	}

	if group == "douban" || group == "weibo" {
		processMulti("collector.sources", group)
	} else if group == "" {
		if allowed["collector.sources"] {
			updates["collector.sources"] = joinFormValues(form["collector.sources"])
		}
	}

	if group == "feishu" || group == "pushplus" {
		processMulti("notifier.channels", group)
	} else if group == "" {
		if allowed["notifier.channels"] {
			updates["notifier.channels"] = joinFormValues(form["notifier.channels"])
		}
	}
	return updates
}

var secretKeys = map[string]bool{
	"secret.filter.llm.api_key":      true,
	"secret.filter.llm.base_url":     true,
	"secret.collector.douban.cookie": true,
	// setup 阶段留空表示“沿用已存值”，因此 cookie_raw 也必须纳入 secretKeys
	"secret.collector.douban.cookie_raw": true,
	"secret.collector.weibo.cookie":  true,
	"secret.collector.weibo.cookie_raw": true,
	"secret.notifier.pushplus.token": true,
	"secret.notifier.feishu.webhook": true,
	"admin.token":                    true,
}

func isCheckbox(key string) bool {
	return key == "filter.ai_enabled" || key == "admin.auth_required"
}

func mergeToggleCSV(csv, item string, on bool) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range splitFlexible(csv) {
		if p == item || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if on {
		out = append(out, item)
	}
	return strings.Join(out, ",")
}

func joinFormValues(values []string) string {
	var parts []string
	for _, x := range values {
		parts = append(parts, splitFlexible(x)...)
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

func (s *Server) saveUpdates(updates map[string]string) (bool, error) {
	if len(updates) == 0 {
		return false, fmt.Errorf("没有有效的配置项")
	}
	for k, v := range updates {
		updates[k] = config.NormalizeValue(v)
	}
	current := CurrentConfigKV(s.db)
	needRestart := len(ChangedRestartKeys(current, updates)) > 0
	merged := config.MergeKV(current, updates)
	app := config.KVToApp(merged)
	if errs := config.ValidateApp(app); len(errs) > 0 {
		return false, fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	env := config.KVToSecrets(merged)
	if errs := config.ValidateSecrets(env); len(errs) > 0 {
		return false, fmt.Errorf("校验失败: %s", strings.Join(errs, "; "))
	}
	if err := store.SetConfigBatch(s.db, updates); err != nil {
		return false, err
	}
	if err := s.rt.ReloadOnce(); err != nil {
		return needRestart, err
	}
	return needRestart, nil
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
