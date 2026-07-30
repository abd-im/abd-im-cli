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
| [ ] | ABD-011 | blocked | US-01 | 在 `internal/reply/` 实现 event-bound reply、reply slot 与消息 operation/idempotency。 | ABD-005, ABD-006, ABD-008, ABD-010 | 一 event 一 reply slot；崩溃后只得到 `confirmed`、`failed` 或 `unknown`，不自动补发。 |

**Checkpoint**：US-01 可以独立演示，并满足 SC-002、SC-003、SC-006 和 SC-007。

## Phase 4: US-02 受限 IM capability 执行面

**目标**：Agent 只能调用 manifest 与 run grant 共同允许的 typed 方法；`group.create` 是首个写入 handler。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-012 | blocked | US-02 | 在 `internal/capability/`、`internal/operation/` 和 `internal/capability/groupcreate/` 实现 capability manifest、通用副作用守卫、run-scoped `group.create` handler 与 operation/idempotency。 | ABD-005, ABD-006, ABD-009, ABD-010 | 只有 manifest 与 grant 共同允许的方法可执行；成员 ID 必须在 allowlist；未知结果不能用新 key 自动重建群。 |

**Checkpoint**：通用 capability 执行面和首个写入 handler 可独立演示，并满足 SC-005。

## Phase 5: US-02/US-03 typed 查询能力与 Owner 诊断

**目标**：共享 typed read service 同时支持 owner 查询和 provider 的受限工具调用，且不读取 SDK 数据库。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-013 | blocked | US-02, US-03 | `[P]` 在 `internal/service/profile/` 实现 profile、自身、用户、daemon 与 doctor typed read service。 | ABD-006, ABD-007 | 每个响应有 schema、`stale` 和 capability 状态。 |
| [ ] | ABD-014 | blocked | US-02, US-03 | `[P]` 在 `internal/service/conversation/` 实现会话 typed read service、分页和 cursor。 | ABD-006, ABD-007 | conversation 查询遵守 limit、cursor 和 `stale` 语义。 |
| [ ] | ABD-015 | blocked | US-02, US-03 | `[P]` 在 `internal/service/message/` 实现消息 history/search/get。 | ABD-006, ABD-007 | 所有消息读取都受 limit、cursor 和 grant 消息窗口约束。 |
| [ ] | ABD-016 | blocked | US-02, US-03 | `[P]` 在 `internal/service/group/` 实现群和成员 typed read service。 | ABD-006, ABD-007 | 每个公开群查询都有 schema、capability 状态和 SDK integration test。 |
| [ ] | ABD-017 | blocked | US-02, US-03 | `[P]` 在 `internal/service/social/` 实现好友和黑名单 typed read service。 | ABD-006, ABD-007 | 每个公开查询都有 schema、scope 检查和 capability 状态。 |

## Phase 6: MCP 与 P1 发布门禁

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-018 | blocked | US-03 | `[P]` 在 `internal/mcp/owner/` 实现 owner MCP 的 typed stdio adapter。 | ABD-007, ABD-012 至 ABD-017 | MCP 不暴露任意 CLI/RPC；只调用同一 daemon service interface。 |
| [ ] | ABD-019 | blocked | US-01, US-02 | `[P]` 在 `internal/mcp/provider/` 实现 provider MCP/tool proxy typed adapter。 | ABD-009, ABD-012 至 ABD-017 | P1 暴露 manifest 与 grant 共同允许的读取工具和已验证 action handler，无 endpoint 覆盖。 |
| [ ] | ABD-020 | blocked | 全部 | 在 `tests/e2e/` 编写 P1 端到端、崩溃恢复、权限和隐私回归。 | ABD-008 至 ABD-019 | SC-001 至 SC-008 均可自动验证。 |

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。US-02 先交付 capability 执行面和首个 action handler，再由共享 typed read service 同时补齐 US-02 与 US-03；`ABD-020` 是 P1 发布门禁。
