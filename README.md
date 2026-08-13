# rent-scout

豆瓣租房帖自动采集、规则筛选、多渠道通知。配置全部存 SQLite，通过 Web 控制台管理。

**变更记录**

| 版本 | 时间 | 变更说明 |
|------|------|----------|
| v0.1 | 2026-08-13 | 初版 README（快速开始 / Docker） |
| v0.2 | 2026-08-13 | IA（三顶栏+六 tab）、热加载矩阵、Cookie raw/探测、日志约定；修正规则入口 |
| v0.3 | 2026-08-13 11:35:00 | Spec 09：三规则类型、硬链白→黑→AI、Post 四态真相表、NotifyBatch/Item/handled `/h`、§7 日志与 hash-COW |
| v0.4 | 2026-08-13 11:40:00 | 架构说明改为仓库根 `ARCHITECTURE.md`（`docs/` 被 gitignore，避免死链） |

> **当前版本：v0.4**

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

**一级顶栏（仅三）：**

| 标签 | 路径 |
|------|------|
| 帖子 | `/admin` |
| 统计 | `/admin/stats` |
| 配置 | `/admin/config`（默认 `?tab=general`） |

**配置二级 Tab（`?tab=`）：** `general` · `sources` · `rules` · `filter` · `notifier` · `admin`

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

| mode | 说明 |
|------|------|
| `none` | 匿名（默认） |
| `raw` | 粘贴 cookie 原文（优先）；页面不回显全文，可显示「已保存 · 长度 N」 |
| `file` | 本地文件路径 |
| `cookiecloud` | 云同步 |

**探测：** 配置页 `POST /admin/config/cookie/test` 用草稿探测（解析 + 轻量在线 GET），**不写库**；日志仅 `cookie_len` / `status`，无明文。

## 热加载矩阵 vs RestartKeys

Runtime 约每 10s 轮询 SQLite：**先 hash，变化才 COW**；未变跳过并打 `[hot_config_skip]`。保存后也可立即 `ReloadOnce`。

| 项 | 期望 |
|----|------|
| interval / jitter / max_age、admin token、rules | 热生效 |
| Cookie raw/file/cloud、Douban groups | 下次 Get / 下一轮跟 Runtime |
| Cookie Test | 即时，不写库 |
| LLM、webhook、`server.addr`、`log.path`、`collector.sources` 列表 | **需重启**（与 `RestartKeys` 一致；保存后黄条提示） |
| 反馈签名密钥 | 跟 Runtime 当前 admin token |

## 日志约定

业务日志带 `component` ∈ `main|hot_config|collector|filter|notifier|admin|setup`，消息形态为 `[{event_tag}] {中文说明}`。热加载示例：`[hot_config_load] 配置变更，开始 COW 更换快照`、`[hot_config_skip] 配置 hash 未变，跳过 COW`、`[hot_config_load_failed] 配置重载失败`。禁止打印完整 cookie / token / LLM key。详见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## 配置说明

- 所有配置（含 webhook、LLM key）写入 SQLite `kv_config` 表
- 敏感项以 `secret.` 前缀存储，页面展示打码
- 无配置文件，无需 `config.toml`
- 仓库当前**不含** `db/demo.sql`；需要样例数据请自行写入 SQLite

## 开发

```bash
go test ./...
go build ./cmd/rent-scout
```

架构说明见 [ARCHITECTURE.md](ARCHITECTURE.md)。
