# Tasks: abdim-cli

状态：活动 backlog。范围以 [`spec.md`](spec.md) 为准。

本文件只保留状态为 `ready`、`blocked` 或 `in_progress` 的 task。完成或取消的 task 必须在状态变更当天移至 `archive/tasks/YYYY-MM-DD-tasks.md`。`P0`、`P1` 是交付阶段，不是 task 状态；不在此文件记录临时 owner 或 claim。

**输入**：[`spec.md`](spec.md) 的 US、FR 和 SC。

**测试**：每个实现 task 的完成条件都必须有自动化验证；P1 发布门禁由 `ABD-032` 至 `ABD-034` 共同执行。

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
| [ ] | ABD-035 | ready | US-02 | 在 `internal/capability/groupcreate/`、`internal/connector/` 和 `cmd/abdim/` 将 `group.create` 接至 daemon-owned、经认证的 server action source，并建立 integration gate。 | ABD-012, ABD-024 | SDK 本地同步 API 不可达；handler 仅由 manifest 与 grant 共同开放，server 请求只带 owner 和 allowlisted member IDs。 |
| [ ] | ABD-036 | ready | US-02 | 在 `tests/e2e/` 验证 provider 的 grant-bound typed message reads、会话/目标限制与消息窗口。 | ABD-009, ABD-015, ABD-025, ABD-027 至 ABD-031 | SC-004 可自动验证。 |
| [ ] | ABD-037 | ready | US-02 | 在 `tests/e2e/` 验证 `group.create` 的 allowlist、operation/idempotency 与未知结果恢复。 | ABD-035 | SC-005 可自动验证。 |
| [ ] | ABD-034 | ready | US-01, US-02 | 在 `tests/e2e/` 验证 provider 隔离、撤销/权限变化/过期取消和 token/message privacy 回归。 | ABD-003, ABD-009 至 ABD-011, ABD-025 至 ABD-026, ABD-032, ABD-036, ABD-037 | SC-006 至 SC-008 可自动验证。 |

**当前状态**：`ABD-024` 至 `ABD-032` 已完成 daemon、provider deployment boundary、全部 P1 typed server-read source 接线和 runtime/inbound e2e gate；每个可用 source 都有固定 SDK/server integration gate。OpenIM 未公开 server unread count，故 `conversation.unread` 继续 `not_validated`。发现 `group.create` 尚未接入 daemon-owned action source，故原 `ABD-033` 已拆为 `ABD-035` 至 `ABD-037`；它们与 `ABD-034` 是首个发布版本剩余的门禁。

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。US-02 先交付 capability 执行面和首个 action handler，再由共享 typed read service 同时补齐 US-02 与 US-03；`ABD-024` 至 `ABD-032` 已完成 daemon、provider MCP、部署边界、全部 P1 server-read source 及 lifecycle/inbound e2e。`ABD-035` 完成后，`ABD-036` 与 `ABD-037` 可独立执行；`ABD-034` 在两者完成后组成发布门禁。
