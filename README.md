# rent-scout

豆瓣租房帖自动采集、规则筛选、多渠道通知。配置全部存 SQLite，通过 Web 控制台管理。

**变更记录**

| 版本 | 时间 | 变更说明 |
|------|------|----------|
| v0.1 | 2026-08-13 | 初版 README（快速开始 / Docker） |
| v0.2 | 2026-08-13 | IA（三顶栏+六 tab）、热加载矩阵、Cookie raw/探测、日志约定；修正规则入口 |
| v0.3 | 2026-08-13 11:35:00 | Spec 09：三规则类型、硬链白→黑→AI、Post 四态真相表、NotifyBatch/Item/handled `/h`、§7 日志与 hash-COW |
| v0.4 | 2026-08-13 11:40:00 | 架构说明改为仓库根 `ARCHITECTURE.md`（`docs/` 被 gitignore，避免死链） |
| v0.5 | 2026-08-13 12:00:00 | Plan 10：包分层（collector sources / store·admin 子包）；Cookie 去掉 file，UI 按 mode 联动 |
| v0.6 | 2026-08-13 12:10:00 | 架构说明迁回 `docs/ARCHITECTURE.md`；gitignore 仅忽略 docs 草稿、放行架构 |
| v0.7 | 2026-08-13 12:05:00 | config：EnvLocal→Secrets、Runtime→HotConfig |
| v0.8 | 2026-08-13 12:20:00 | cookie 子包、notifier/channels、架构依赖表 |
| v0.9 | 2026-08-13 14:45:00 | `docs/` 整目录不入库；Cookie 探测展示脱敏豆瓣 cookie、风控间隔符、探测打小组 URL |
| v0.10 | 2026-08-13 15:15:00 | 日志 `[]` 只标协程职责（`hot_config_reload` / `douban_collector` / `notifier` 等），消息不再写 event_tag |
| v0.11 | 2026-08-13 15:50:00 | CookieCloud 对齐 AES-128-CBC（gongji 向量）；探测类打 raw 请求应答；顶栏「日志」SSE 滚动 |
| v0.12 | 2026-08-13 15:55:00 | 内存日志默认 1000 条；配置→常规可改 `log.memory_lines`（100–10000，热生效），并提示内存占用 |
| v0.13 | 2026-08-13 16:10:00 | 源进度：`source_state.cursor` 存 JSON（backfill 翻页 + incremental 水位）；时间窗/小组变更自动重置；`POST /api/sources/{name}/reset` |
| v0.14 | 2026-08-13 16:20:00 | 豆瓣拉取范围改为天数「从/至」（默认 -10 / now）；规则 autosave 修 400；配置导出 JSON；CookieCloud / 豆瓣检测拆开 |
| v0.15 | 2026-08-13 16:40:00 | `douban_collector` 只读本地 cookie；`douban_cookie_cloud` 每 10 分钟同步到 `cookie_raw`；源/CookieMode 改枚举；CookieCloud 日志按请求→应答→过滤域→解密→拿 cookie |
| v0.16 | 2026-08-13 16:50:00 | 筛选 `filter` 有帖立刻硬规则落库，不等批；AI 审核 `ai_review` 从库读 pending 凑批再调 LLM |
| v0.17 | 2026-08-13 17:00:00 | 采集日志只保留开始/结束等待下一轮；豆瓣 3s 是请求间隔不再当轮次；配置按关注点分色块，Cookie/检测就近，保存单独靠右 |

> **当前版本：v0.17**

---

## 快速开始

```bash
go run ./cmd/rent-scout
```

首次启动日志会提示 SQLite 配置为空，浏览器访问 `http://localhost:7777/admin/setup` 完成引导。

**引导流程：** 步骤 1 鉴权（必填，保存后继续）→ 步骤 2–5 可跳过，各步独立保存到 SQLite。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `DB_PATH` | `db/rent-scout.db` | SQLite 数据库路径 |

## Docker

```bash
docker compose up -d
```

数据持久化在 `./data/` 目录。

## 管理页面（信息架构）

**一级顶栏（四入口）：**

| 标签 | 路径 |
|------|------|
| 帖子 | `/admin` |
| 统计 | `/admin/stats` |
| 配置 | `/admin/config`（默认 `?tab=general`） |
| 日志 | `/admin/logs`（内存 ring + SSE 实时滚动） |

**配置二级 Tab（`?tab=`）：** `general` · `sources` · `rules` · `ai` · `notifier` · `admin`

- 规则在 **`/admin/config?tab=rules`**，不是独立顶栏页。
- `GET /admin/rules` → `302` 到 `?tab=rules`；`POST /admin/rules*` 仍保留。
- 首次引导：`/admin/setup`

## 规则（三类型）

| `type` | 含义 |
|--------|------|
| `whitelist` | 命中通过并写入地点标签 `address_tags` |
| `blacklist` | 任一命中拒绝 |
| `ai_natural` | 自然语言，交 AI |

**硬链顺序**：白名单 → 黑名单 →（未定案）AI。黑名单未命中不自动通过。同条逗号为「或」；禁止理解为「多规则之间且」。`mode` / `collect_tags` 已废弃。

## Post 四态与真相表

帖子主状态仅：`collected` / `pending` / `passed` / `rejected`（无 `sent`/`acked` 主状态语义）。

| 关心点 | 唯一真相 |
|--------|----------|
| 筛选是否通过 | `posts.status` + `filter_results` |
| 某渠道是否发出 | `notifications(post_id,channel).status` |
| 用户有用/无用 | `feedbacks` |
| 运营已处理 | `posts.handled_at` |
| 通知分组键 | `posts.address_tags[0]`（空 →「未分组」） |

## 通知（NotifyBatch / Item / 已处理）

- 发送路径组装 **NotifyBatch**（`group_key` + `items`），Item 含源链接、有用/无用反馈、已处理链接。
- 反馈：`/f` 签名 → 写 `feedbacks`。
- 已处理：`GET /h` 签名 → 写 `handled_at`，不写反馈；控制台也可 `POST /admin/handled`。

## Cookie（采集 / 源 Tab）

| mode | 展示字段 |
|------|----------|
| `none` | 无附加字段（默认） |
| `raw` | Cookie 原文 textarea（+ 已保存长度 hint；不回显全文） |
| `cookiecloud` | url / key / password |

旧 `file` 模式已移除；库内 `cookie_mode=file` 会规范为 `none`。

**探测：** 配置页 / Setup `POST /admin/config/cookie/test` 用草稿探测（解析 + 轻量在线 GET），**不写库**。CookieCloud 只列出豆瓣域 cookie（脱敏预览）；在线探测优先打第一组小组 URL。CookieCloud 解密对齐官方/gongji：`encrypted` 字段，`key=md5(uuid-password)[:16]`，先 **AES-128-CBC（IV=0）**，失败再 Salted__ AES-256。探测 HTTP 打 raw 请求应答（Cookie/Authorization 脱敏），可在「日志」页边测边看。

## 热加载矩阵 vs RestartKeys

HotConfig 约每 10s 轮询 SQLite：**先 hash，变化才 COW**；未变跳过并打 `[hot_config_skip]`。保存后也可立即 `ReloadOnce`。

| 项 | 期望 |
|----|------|
| interval / jitter / max_age、admin token、rules | 热生效 |
| Cookie raw/cookiecloud、Douban groups | 下次 Get / 下一轮跟 HotConfig |
| Cookie Test | 即时，不写库 |
| LLM、webhook、`server.addr`、`log.path`、`collector.sources` 列表 | **需重启**（与 `RestartKeys` 一致；保存后黄条提示） |
| 反馈签名密钥 | 跟 HotConfig 当前 admin token |

## 日志约定

`[]` 只标协程职责，不是消息正文。文本形如 `[hot_config_reload] 配置变更，开始 COW 更换快照`；JSON 用 `duty` 字段。固定职责名：`main` · `hot_config_reload` · `douban_collector`（按源 `{source}_collector`）· `filter` · `notifier` · `admin` · `setup`。禁止打印完整 cookie / token / LLM key。

管理台 `/admin/logs` 用进程内 ring + SSE（`/admin/logs/stream`）做动态滚动，不引入 Loki 等外部栈。默认保留 **1000** 条，可在 **配置 → 常规 → 内存日志条数**（`log.memory_lines`，100–10000）调整，保存后立即生效。条数越大越占内存：普通日志大约几百 KB，探测 raw 多时 1000 条可能到数 MB。Cookie / AI 探测的 `stage`、req/resp raw 都会进这里。

## 采集进度（source offset）

每源独立 goroutine（如 `douban_collector`）按页写入 `source_state`：

| 阶段 | 行为 |
|------|------|
| `backfill` | 从 `page` 游标翻历史，直到时间窗下沿或各组走完 |
| `incremental` | 每轮从列表头抓新帖，碰到 `watermark`（已见最新发布时间）即停，不再打旧页 |

时间窗每次按配置相对 `now` 解析（默认 `-10d`～`now`），这是过滤窗，不是翻页起点。改 `range_from/to` 或小组列表会重置进度；也可 `POST /api/sources/{name}/reset`。旧纯字符串游标仍能读，当成 backfill 的 page。

## 配置说明

- 所有配置（含 webhook、LLM key）写入 SQLite `kv_config` 表（扁平 key，如 `collector.douban.groups`、`secret.notifier.feishu.webhook`）
- 源/渠道差异用 Go 结构体字段 + 扁平 KV，**没有**按源子表，也**没有**整段 JSON blob 存配置；运行时进度才是 JSON（见上）
- 敏感项以 `secret.` 前缀存储，页面展示打码
- 无配置文件，无需 `config.toml`
- 仓库当前**不含** `db/demo.sql`；需要样例数据请自行写入 SQLite

## 开发

```bash
go test ./...
go build ./cmd/rent-scout
```

架构说明在本地 `docs/`（不入库）。
