# Tasks: abdim-cli

状态：活动 backlog。范围以 [`spec.md`](spec.md) 为准。

本文件只保留状态为 `ready`、`blocked` 或 `in_progress` 的 task。完成或取消的 task 必须在状态变更当天移至 `archive/tasks/YYYY-MM-DD-tasks.md`。`P0`、`P1` 是交付阶段，不是 task 状态；不在此文件记录临时 owner 或 claim。

**输入**：[`spec.md`](spec.md) 的 US、FR 和 SC。

**测试**：每个实现 task 的完成条件都必须有自动化验证；P1 发布门禁由 `ABD-032`、`ABD-038` 至 `ABD-040` 共同执行。

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

## Phase 7: P2 受限写入能力

**目标**：在不扩张 provider 权限边界的前提下，逐项交付 P2 typed action handler。

| 完成 | ID | 状态 | 场景 | 任务与路径 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-043 | ready | US-02 | 在 `internal/capability/message/`、`internal/bridge/abdim/`、`internal/connector/`、`internal/mcp/provider/`、`cmd/abdim/` 和 `tests/e2e/` 实现 run-scoped `message.send_quote`。 | ABD-042 | 显式 grant 只能向 allowlisted 单聊用户或群发送；被引用消息必须在 grant 的同一会话消息窗口内；相同 key 返回原 operation，未知结果不补发；固定 SDK/server integration gate 通过。 |
| [ ] | ABD-044 | ready | US-02 | 在 `internal/capability/message/`、`internal/bridge/abdim/`、`internal/connector/`、`internal/mcp/provider/`、`cmd/abdim/` 和 `tests/e2e/` 实现 run-scoped `message.send_at`。 | ABD-042 | 显式 grant 同时约束目标群和每个被 @ 用户，文本和 mention 数量有上限；相同 key 与未知结果语义保持 fail closed；固定 SDK/server integration gate 通过。 |
| [ ] | ABD-045 | ready | US-02 | 在 `internal/capability/conversation/`、`internal/bridge/abdim/`、`internal/connector/`、`internal/mcp/provider/`、`cmd/abdim/` 和 `tests/e2e/` 实现 run-scoped `conversation.mark_read`。 | ABD-041 | 显式 grant 只能标记 allowlisted conversation 内、消息窗口所界定的已读边界；相同 key 返回原 operation，未知结果不补发；固定 SDK/server integration gate 通过。 |
| [ ] | ABD-046 | ready | US-02 | 在 `internal/capability/message/`、`internal/control/`、`internal/profile/` 和 `tests/e2e/` 建立受 grant 限制的消息附件 metadata、quota 与不透明引用基础设施。 | ABD-041 | provider 不能传递任意本地路径；每个附件引用绑定 profile、run、额度和有效期；正文与文件内容不进入 control DB、审计或日志。 |

**当前状态**：`ABD-024` 至 `ABD-032`、`ABD-035` 至 `ABD-042` 已完成 daemon、provider deployment boundary、全部 P1 typed server-read/action source 及 runtime/inbound、grant-bound message、action recovery、provider isolation、cancellation、privacy e2e gate，以及 P2 的 `message.send_text`。OpenIM 未公开 server unread count，故 `conversation.unread` 继续 `not_validated`。`group.create` 与 `message.send_text` 只在 manifest、显式 grant 和对应 action handler 共同允许时公开；默认入站 policy 仍只授予 `message.history`。P3 的消息媒体、会话、社交和群组写入会在各自固定 SDK/server action 已确认后，按一个 typed method 一个 task 的规则进入本清单。

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。US-02 先交付 capability 执行面和首个 action handler，再由共享 typed read service 同时补齐 US-02 与 US-03；`ABD-024` 至 `ABD-032`、`ABD-035` 至 `ABD-042` 已完成 daemon、provider MCP、部署边界、全部 P1 server-read/action source 及 lifecycle/inbound/grant/action/isolation/cancellation/privacy e2e。P2 先完成消息引用、群 @、已读和附件边界；P3 只在相应固定 action source 已确认后，再按 IM 领域和一个 typed method 一个 task 的规则推进。
