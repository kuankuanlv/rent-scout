# 调度硬约束（omo-slim）

> 此文件通过 `opencode.json: instructions: ["rules/*"]` 自动加载为全局指令，重启 opencode 后生效。

## 1. 有计划必按计划

- 若 `docs/superpowers/plans/*.md` 已存在，任何实现该功能的需求**必须**通过 `superpowers:subagent-driven-development` 按任务执行：`task-brief → fixer-ominiroute → review-package → oracle-ominiroute → progress.md`。
- 禁止绕过计划直接派 `designer/fixer` 改代码，即使结果一致也视为违规。

## 2. 直改禁止产计划

- 当需求明确要求 `直接实现` / `不要写计划` 时，禁止调用 `superpowers:brainstorming` / `superpowers:writing-plans`。
- 此时仅允许 `Read → Edit/Write → go vet/test` 闭环。

## 3. 调度命名契约

- 只能使用 `*-ominiroute`（如 `designer-ominiroute` / `fixer-ominiroute` / `explorer-ominiroute` / `oracle-ominiroute`），禁止 `*-free` 或裸名。

## 4. 验证门禁

- UI 变更必须有 `designer-ominiroute` 的 `review-package` 证据；后端变更必须有 `fixer-ominiroute` 或带输出的 `bash` 证据。
- 未经 `task-brief` 的实现不得标记完成。

违反以上任一条即视为调度失败，需重派并记入复盘。
