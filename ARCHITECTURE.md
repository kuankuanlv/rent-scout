# rent-scout 架构

- **日期**：2026-08-13
- **范围**：模块、流水线、热加载、日志约定、Post/Rule/Notify 契约
- **状态**：与 Spec 08 / Spec 09 当前实现对齐

**变更记录**

| 版本 | 时间 | 变更说明 |
|------|------|----------|
| v0.1 | 2026-08-13 10:00:00 | 初版：模块、流水线、热加载矩阵、IA、Cookie、日志约定 |
| v0.2 | 2026-08-13 10:07:00 | 日志约定：`cookie_test` 归属改为 admin（配置页 Cookie 探测） |
| v0.3 | 2026-08-13 11:35:00 | Spec 09：三规则类型、硬链白→黑→AI、Post 四态真相表、NotifyBatch/Item/handled `/h`、§7 `[tag] 中文` + hash-COW |
| v0.4 | 2026-08-13 11:40:00 | 同步至仓库根目录，便于 git 跟踪（`docs/` 仍 gitignore） |

> **当前版本：v0.4** — 以本文件与 Spec 09 为准（热加载/IA 与 Spec 08 正交条款并存）。

---

## 模块

```
cmd/rent-scout          启动、装配 collector/filter/notifier/admin
internal/config         Runtime COW 快照 + WatchDB 热加载
internal/collector      源适配（douban）+ CookieProvider + Runner
internal/filter         硬编码规则链 + AI 批
internal/notifier       多渠道推送 + 死信
internal/admin          HTTP 控制台（帖子/统计/配置）
internal/store          SQLite
internal/pkglog         slog 初始化 + Component 薄封装
```

## 流水线

```
collector.Runner ──trigger──► filter.Consumer ──notifyTrigger──► notifier
        │                           │                              │
        └─────────────── SQLite (posts / rules / notifications) ───┘
                                      │
                               admin HTTP (:7777)
```

1. **采集**：每源独立 goroutine；List → 时间窗 → 查重 → Detail → 入库 → `trigger_sent`
2. **筛选**：硬链白名单 → 黑名单 →（未定案）AI → `post_decided`；passed 触发通知
3. **通知**：按 `address_tags[0]` 组装 `NotifyBatch` → 渠道发送；失败重试至 `dead_letter`

## 配置与热加载

- **唯一来源**：SQLite `kv_config`（`secret.*` 为敏感项）
- **Runtime**：`ReloadOnce` / `WatchDB(10s)`；**先有序 `hashKV`，hash 变化才 COW**；未变禁止 Store 新指针，打 `[hot_config_skip]`（Debug）
- **显式 ReloadOnce**（Admin 保存 / Setup）：允许直接重载 COW；成功打 `[hot_config_load]`
- **首次引导**：`/admin/setup` → `setup.completed=true`

### 热加载矩阵（与 `RestartKeys`）

| 项 | 期望 |
|----|------|
| interval/jitter/max_age、admin.token、rules | 热生效 |
| Cookie、Douban groups | 下次 Get / 下一轮 |
| Cookie Test | 即时，不写库 |
| LLM、webhook、server.addr、log.path、sources 列表 | **需重启**（`admin.RestartKeys`） |
| FeedbackSecret | 跟 Runtime |

## 控制台 IA

- 顶栏三入口：帖子 `/admin` · 统计 `/admin/stats` · 配置 `/admin/config`
- 配置六 tab：`general` · `sources` · `rules` · `filter` · `notifier` · `admin`
- 规则 UI 在 `?tab=rules`；`GET /admin/rules` 仅 302

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

`none` | `raw`（粘贴优先）| `file` | `cookiecloud`；探测草稿不写库；日志无明文。

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
