# Tasks: abdim-cli

状态：活动 backlog。范围以 [`spec.md`](spec.md) 为准。

本文件只保留状态为 `ready`、`blocked` 或 `in_progress` 的 task。完成或取消的 task 必须在状态变更当天移至 `archive/tasks/YYYY-MM-DD-tasks.md`。`P0`、`P1` 是交付阶段，不是 task 状态；不在此文件记录临时 owner 或 claim。

**输入**：[`spec.md`](spec.md) 的 US、FR 和 SC。

**测试**：每个实现 task 的完成条件都必须有自动化验证；P1 发布门禁由 `ABD-032`、`ABD-038` 至 `ABD-040` 共同执行。

**格式**：`[P]` 表示在其依赖完成后可与其他 `[P]` task 并行，`US-*` 追溯到用户场景。实现所有权以 [`ARCHITECTURE.md`](ARCHITECTURE.md) 的能力领域映射为准；本清单和 issue record 只描述交付目标、依赖和验收。

**并行规则**：不同能力领域且不共享注册表文件的 `[P]` task 可并行；能力族内部的方法和验收应作为一个可交付结果完成。共享 `cmd/abdim`、provider MCP 静态 registry 或 daemon method assembly 的收口必须串行执行。

## Phase 2: Foundation

本阶段完成前不得实现用户场景。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：P0 foundation 可在无真实凭据的环境中验证 FR-001 至 FR-008。

## Phase 3: US-01 入站对话闭环

**目标**：一条允许的入站消息只创建一个 run，并由 daemon 回复触发会话。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：US-01 可以独立演示，并满足 SC-002、SC-003、SC-006 和 SC-007。

## Phase 4: US-02 受限 IM capability 执行面

**目标**：Agent 只能调用 manifest 与 run grant 共同允许的 typed 方法；`group.create` 是首个写入 handler。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**Checkpoint**：通用 capability 执行面和首个写入 handler 可独立演示，并满足 SC-005。

## Phase 5: US-02/US-03 typed 查询能力与 Owner 诊断

**目标**：共享 typed read service 同时支持 owner 查询和 provider 的受限工具调用，且不读取 SDK 数据库。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

## Phase 6: MCP 与 P1 发布门禁

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-063 | ready | Release | 选择项目许可证并加入根目录 `LICENSE`。 | ABD-062 | 项目所有者确认许可证；release archive 包含许可证；`docs/RELEASING.md` 不再将缺少许可证列为公开发布 blocker。 |

## Phase 7: P2 受限写入能力

**目标**：在不扩张 provider 权限边界的前提下，逐项交付 P2 typed action handler。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

## Phase 8: P3 会话、关系与群组管理

**目标**：按可演示的 IM 能力族交付会话设置、好友/黑名单和群组管理；每个 issue 内的 typed method 共享授权边界和 integration gate。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

## Phase 9: P4 Provider and Run Operations

**目标**：在不改变 P1 reply target、调用模型和授权边界的前提下，交付单 Codex provider 的兼容性证据和高级 run 运维。多 provider 与 session migration 暂缓。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |

**当前状态**：`ABD-024` 至 `ABD-062`（不含 deferred 的 `ABD-054`、`ABD-056`）已完成 daemon、typed server-read/action source、消息控制、媒体、会话设置、好友/黑名单、群组管理、compatibility evidence、owner run 运维、GitHub Release 自动化、当前用户 Codex runtime、单命令首次设置和可配置入站工具边界。`abdim setup` 登录固定 ABD 部署并自动启动当前用户 daemon，不配置第二个 IM 身份或配对步骤；日常生命周期为 `start`、`stop`、`restart`、`status`。公开 policy 只接受非 self 的有效私聊；默认发放空 methods/scopes 和单次预算，`inbound tools enable` 后发放全部已验证 typed tools、方法级 target 通配、触发私聊历史窗口和受限预算，群聊默认忽略。该开关对全部私聊 sender 生效，不提供逐次审批。每个 run 仍有独立 `CODEX_HOME`、固定 MCP 配置、grant 和 event-bound reply，但同 UID 不被描述为 OS 级隔离。OpenIM 未公开 server unread count，故 `conversation.unread` 继续 `not_validated`。多 provider `ABD-054` 与 session migration `ABD-056` 保持 deferred。`ABD-063` 是公开发布前仍需项目所有者决定的许可证任务。

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。`ABD-048` 与 `ABD-052` 在领域实现和验证阶段并行完成，`ABD-053` 在 `ABD-052` 完成后交付；`ABD-055` 与 `ABD-057` 随后并行完成并在共享装配点串行收口，`ABD-058` 交付 CI、受控 integration 和 Unix tag release，`ABD-059` 完成当前用户 runtime 收口，`ABD-060` 收敛单命令设置，`ABD-061` 删除未要求的配对门槛，`ABD-062` 交付默认 reply-only、显式全工具开关和完整受控证据门禁。`ABD-063` 在项目所有者选择许可证后执行；`ABD-054`、`ABD-056` 暂缓。共享注册表的修改不再并行写入。
