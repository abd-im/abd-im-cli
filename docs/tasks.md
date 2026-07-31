# Tasks: abdim-cli

状态：活动 backlog。范围以 [`spec.md`](spec.md) 为准。

本文件只保留状态为 `ready`、`blocked` 或 `in_progress` 的 task。完成或取消的 task 必须在状态变更当天移至 `archive/tasks/YYYY-MM-DD-tasks.md`。`P0`、`P1` 是交付阶段，不是 task 状态；不在此文件记录临时 owner 或 claim。

**输入**：[`spec.md`](spec.md) 的 US、FR 和 SC。

**测试**：每个实现 task 的完成条件都必须有自动化验证；P1 发布门禁由 `ABD-020` 统一执行。

**格式**：`[P]` 表示在其依赖完成后可与其他 `[P]` task 并行，`US-*` 追溯到用户场景。路径是该 task 的预期代码所有权，变更路径时必须保留同等隔离和验收条件。

## Phase 2: Foundation

本阶段完成前不得实现用户场景。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：P0 foundation 可在无真实凭据的环境中验证 FR-001 至 FR-008。

## Phase 3: US-01 入站对话闭环

**目标**：一条允许的入站消息只创建一个 run，并由 daemon 回复触发会话。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：US-01 可以独立演示，并满足 SC-002、SC-003、SC-006 和 SC-007。

## Phase 4: US-02 受限 IM capability 执行面

**目标**：Agent 只能调用 manifest 与 run grant 共同允许的 typed 方法；`group.create` 是首个写入 handler。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：通用 capability 执行面和首个写入 handler 可独立演示，并满足 SC-005。

## Phase 5: US-02/US-03 typed 查询能力与 Owner 诊断

**目标**：共享 typed read service 同时支持 owner 查询和 provider 的受限工具调用，且不读取 SDK 数据库。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

## Phase 6: MCP 与 P1 发布门禁

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-028 | ready | US-02, US-03 | 在 `internal/service/profile/` 和 `internal/connector/` 映射 profile、自身、用户、daemon、doctor 的真实 source 并建立 integration gate。 | ABD-024 | 已映射方法有固定 SDK/server integration；未映射方法保持 `not_validated`，不读取 SDK 数据库。 |
| [ ] | ABD-029 | ready | US-02, US-03 | 在 `internal/service/conversation/` 和 `internal/connector/` 映射会话 server-read source 并建立 integration gate。 | ABD-024 | 会话读取只走 server source，支持 cursor；未验证时 fail closed。 |
| [ ] | ABD-030 | ready | US-02, US-03 | 在 `internal/service/message/` 和 `internal/connector/` 映射消息 history/search/get server-read source 并建立 integration gate。 | ABD-024 | 不读取 SDK 数据库；limit、cursor 和 grant 消息窗口在真实 source 下仍生效。 |
| [ ] | ABD-031 | ready | US-02, US-03 | 在 `internal/service/social/` 和 `internal/connector/` 映射好友和黑名单 server-read source 并建立 integration gate。 | ABD-024 | 公开查询有固定 server integration；scope 与 capability 状态可验证。 |
| [ ] | ABD-020 | blocked | 全部 | 在 `tests/e2e/` 编写 P1 端到端、崩溃恢复、权限和隐私回归。 | ABD-008 至 ABD-021, ABD-025 至 ABD-031 | SC-001 至 SC-008 均可自动验证。 |

**当前状态**：`ABD-024` 已完成 daemon-owned SDK、owner socket、owner MCP 和 typed dispatcher 组装；`ABD-025` 已将每个 Codex run 接至独立 stdio provider MCP/tool proxy；`ABD-026` 已以 root-controlled 部署配置和独立 OS UID/GID 启动 provider；`ABD-027` 已将 group server-read source 作为 owner 可用 capability 接入真实 daemon。profile、conversation、message、social 仍保持 `not_validated`，分别由 ABD-028 至 ABD-031 独立映射；这些服务不得读取 SDK 数据库。`ABD-020` 只在前述 P1 路径完成后解除阻塞。

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。US-02 先交付 capability 执行面和首个 action handler，再由共享 typed read service 同时补齐 US-02 与 US-03；`ABD-024`、`ABD-025`、`ABD-026` 与 `ABD-027` 已完成 daemon、provider MCP、部署边界与 group 接线，ABD-028 至 ABD-031 可在 ABD-024 后独立推进，`ABD-020` 是 P1 发布门禁。
