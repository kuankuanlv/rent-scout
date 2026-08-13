# rent-scout 架构

- **日期**：2026-08-13
- **范围**：模块、流水线、热加载、日志约定、Post/Rule/Notify 契约、包分层
- **状态**：与 Spec 08 / Spec 09 / Plan 10 当前实现对齐

**变更记录**

| 版本 | 时间 | 变更说明 |
|------|------|----------|
| v0.1 | 2026-08-13 10:00:00 | 初版：模块、流水线、热加载矩阵、IA、Cookie、日志约定 |
| v0.2 | 2026-08-13 10:07:00 | 日志约定：`cookie_test` 归属改为 admin（配置页 Cookie 探测） |
| v0.3 | 2026-08-13 11:35:00 | Spec 09：三规则类型、硬链白→黑→AI、Post 四态真相表、NotifyBatch/Item/handled `/h`、§7 `[tag] 中文` + hash-COW |
| v0.4 | 2026-08-13 11:40:00 | 同步至仓库根目录，便于 git 跟踪（`docs/` 仍 gitignore） |
| v0.5 | 2026-08-13 12:00:00 | Plan 10：collector/sources/douban、store/admin 领域子包+门面；Cookie 仅 none/raw/cookiecloud（file→none） |
| v0.6 | 2026-08-13 12:10:00 | 归位 `docs/ARCHITECTURE.md`（gitignore 放行）；HotConfig/Secrets 命名；cookie 子包；渠道 channels |
| v0.7 | 2026-08-13 12:05:00 | config 清晰化：EnvLocal→Secrets、Runtime→HotConfig；文件 codec/hot/types |
| v0.8 | 2026-08-13 12:20:00 | cookie→collector/cookie；notifier/channels；模块依赖与门面约定 |
| v0.9 | 2026-08-13 12:30:00 | 扁平化单文件子包：admin 收回 posts/rules/setup/sources/stats/feedback/config/ui；store 收回 config/rules/stats/filterresult（保留 posts/notify） |
| v0.10 | 2026-08-13 13:40:00 | Plan 10 UI：删 trim_limits；通知 UI 收敛飞书+PushPlus；LLM 草稿探测；启用源多选 |
| v0.11 | 2026-08-13 14:22:58 | 豆瓣拉取范围 range_from/to（动态 now）；组批拆到 filter/notifier.batch_size；linger 常量化 |

> **当前版本：v0.11** — 以本文件与 Spec 09 / Plan 10 为准（热加载/IA 与 Spec 08 正交条款并存）。

---

## 模块

```
cmd/rent-scout                        启动、装配
internal/config                       App + Secrets + HotConfig（COW 热配置）
internal/collector                    Source / Runner
internal/collector/cookie             Cookie Provider / 探测
internal/collector/sources/douban     豆瓣适配器（后续源同级）
internal/filter                       流水线筛选：硬链 + AI（rules 数据在 store）
internal/notifier                     通知编排（Channel 接口、分组、重试）
internal/notifier/channels            各渠道实现（feishu/dingtalk/…）
internal/admin                        Server 组装 + handlers + templates embed
internal/store                        Open / migrate / 门面（rules/stats/config/filter_results 平铺）
internal/store/{posts,notify}         多文件领域子包
internal/pkglog                       slog + Component
```

### 包分层与门面

| 模块 | 对外入口 | 说明 |
|------|----------|------|
| config | `HotConfig` | 热配置唯一门面：`Get` / `Secrets` / `ReloadOnce` / `WatchDB` |
| store | `Store` + 包级 `GetConfigMap` 等 | 子包持 SQL，根门面委托 |
| admin | `Server` / `Handler` | 根包平铺 handlers；`templates/` embed 留根 |
| collector | `Runner` + `Source` | cookie / sources 为实现细节 |
| notifier | `Notifier` + `Channel` | channels 子包实现，根不反向依赖 |
| filter | `Consumer` / `EvaluateHard` | 流水线节点；不改名 rules（rules 是数据域） |

依赖方向（简）：`main → {collector, filter, notifier, admin, config, store}`；`channels → notifier`；`cookie → config`；`admin/* → store/config`；子包不 import 父包门面以外的兄弟（避免环）。

### 命名约定

- **filter**（包）：流水线「筛选」阶段（硬链 + AI）。
- **rules**（store/admin）：规则 CRUD / 表数据，不是流水线包名。
- **HotConfig**：热配置快照；**Secrets**：`secret.*` 敏感项。## 流水线

```
collector.Runner ──trigger──► filter.Consumer ──notifyTrigger──► notifier
        │                           │                              │
        └─────────────── SQLite (posts / rules / notifications) ───┘
                                      │
                               admin HTTP (:7777)
```

1. **采集**：每源独立 goroutine；List → 时间窗（豆瓣 `range_from`/`range_to` 动态解析；其它源 `max_age_days`）→ 查重 → Detail → 入库 → `trigger_sent`
2. **筛选**：硬链白名单 → 黑名单 →（未定案）AI → `post_decided`；passed 触发通知；组批用 `filter.batch_size`，linger 固定 120s
3. **通知**：按 `address_tags[0]` 组装 `NotifyBatch` → 渠道发送；失败重试至 `dead_letter`；组批用 `notifier.batch_size`

## 配置与热加载

- **唯一来源**：SQLite `kv_config`（`secret.*` 为敏感项）
- **HotConfig**：`ReloadOnce` / `WatchDB(10s)`；**先有序 `hashKV`，hash 变化才 COW**；未变禁止 Store 新指针，打 `[hot_config_skip]`（Debug）
- **Secrets**：敏感项（`secret.*`），经 `HotConfig.Secrets()` 只读
- **显式 ReloadOnce**（Admin 保存 / Setup）：允许直接重载 COW；成功打 `[hot_config_load]`
- **首次引导**：`/admin/setup` → `setup.completed=true`

### 热加载矩阵（与 `RestartKeys`）

| 项 | 期望 |
|----|------|
| interval/jitter/max_age、豆瓣 range_from/to、filter/notifier.batch_size、admin.token、rules | 热生效 |
| Cookie、Douban groups | 下次 Get / 下一轮 |
| Cookie Test | 即时，不写库 |
| LLM、webhook、server.addr、log.path、sources 列表 | **需重启**（`admin.RestartKeys`） |
| FeedbackSecret | 跟 HotConfig |

## 控制台 IA

- 顶栏三入口：帖子 `/admin` · 统计 `/admin/stats` · 配置 `/admin/config`
- 配置六 tab：`general` · `sources` · `rules` · `filter` · `notifier` · `admin`
- 规则 UI 在 `?tab=rules`；`GET /admin/rules` 仅 302
- 通知 tab 子 tab：飞书 + PushPlus（其它渠道实现保留，UI 本期隐藏）
- 筛选：无 `trim_limits` 配置；LLM 正文截断用代码常量 `filter.DefaultTrimLimit`
- Cookie / LLM 草稿探测：`POST /admin/config/cookie/test`、`/admin/config/llm/test`、`/admin/config/llm/models`（不写库）

## 规则（三类型）

| `type` | 含义 |
|--------|------|
| `whitelist` | 命中通过并写入 `address_tags`（同条逗号为 OR） |
| `blacklist` | 任一命中拒绝 |
| `ai_natural` | 自然语言交 AI |

**硬链评估顺序**：白名单 → 黑名单 →（未定案）AI。黑名单未命中**不**自动通过。同条逗号为「或」；**不是**「多规则之间且」。`mode` / `collect_tags` 已废弃，UI 不暴露。

**有意行为变更（相对旧四 type）**：旧 `hard_keyword` include → whitelist（打 Tag）；旧 exclude 全未命中自动过 → 现为进 AI/默认。

## Post 状态与真相表

`posts.status` **仅**四态：`collected` \| `pending` \| `passed` \| `rejected`。禁止写入 `sent`/`acked` 作为主状态。

| 关心点 | 唯一真相 |
|--------|----------|
| 筛选是否通过 | `posts.status` ∈ collected/pending/passed/rejected + `filter_results` |
| 某渠道是否发出 | `notifications(post_id,channel).status` |
| 用户有用/无用 | `feedbacks` |
| 运营已处理 | `posts.handled_at`（≠ 反馈） |
| 通知分组键 | `posts.address_tags[0]`（空 →「未分组」） |

## Notify

- **NotifyItem**：内容（title/url/address_tag/…）+ 动作 URL（源、有用/无用反馈、已处理；已读预留）
- **NotifyBatch**：`{ group_key, items }`；组装层必经，可不落库
- **账本** `notifications`：渠道发送状态，不复制帖文
- **反馈**：`/f` + HMAC → `feedbacks`
- **已处理**：签名入口 `GET /h` → 写 `handled_at`，**不**写 feedbacks；控制台另有 `POST /admin/handled`

## Cookie

仅三模式：`none` | `raw`（粘贴原文）| `cookiecloud`（url/key/password）。配置页 / Setup 按 mode 显隐字段。旧 `cookie_mode=file` 启动 migrate 为 `none`（`cookie_file` 键保留但不使用）。探测草稿不写库；日志无明文。

## 日志约定（§7）

业务日志须同时满足：

1. slog 属性 **`component`** ∈ `main|hot_config|collector|filter|notifier|admin|setup`
2. 消息正文：`[{event_tag}] {简短中文说明}`；细节用键值（`keys`/`addr`/`err`…）
3. 禁止完整 cookie / token / LLM key

| component | 代表性 tag |
|-----------|------------|
| hot_config | `hot_config_load` / `hot_config_load_failed` / `hot_config_skip` |
| collector | `round_start` / `round_done` / `trigger_sent` / `cookie_get_empty` |
| filter | `batch_start` / `llm_batch_done` / `post_decided` |
| notifier | `notify_trigger` / `channel_send` / `item_sent` / `dead_letter` |
| admin | `rule_*` / `admin_marked` / `feedback_recorded` / `config_saved` / `cookie_test` |
| main | `boot_*` |

热加载示例：`[hot_config_load] 配置变更，开始 COW 更换快照`、`[hot_config_skip] 配置 hash 未变，跳过 COW`。

实现：`pkglog.Component(name)` → `slog.With("component", …)`。

## 数据表

| 表 | 用途 |
|----|------|
| posts | 采集帖子（四态 + handled_at + address_tags） |
| rules | 筛选规则（whitelist/blacklist/ai_natural） |
| kv_config | 运行时配置 |
| config_history | 配置变更历史 |
| notifications | 推送账本 |
| feedbacks | 用户反馈 |

## 部署

- 单二进制，`DB_PATH` 指定库路径
- Docker：`docker compose up`，数据挂载 `./data`
