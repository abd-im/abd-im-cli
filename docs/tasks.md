# Tasks: abdim-cli

状态：活动 backlog。范围以 [`spec.md`](spec.md) 为准。

本文件只保留状态为 `ready`、`blocked` 或 `in_progress` 的 task。完成或取消的 task 必须在状态变更当天移至 `archive/tasks/YYYY-MM-DD-tasks.md`。`P0`、`P1` 是交付阶段，不是 task 状态；不在此文件记录临时 owner 或 claim。

**输入**：[`spec.md`](spec.md) 的 US、FR 和 SC。

**测试**：每个实现 task 的完成条件都必须有自动化验证；P1 发布门禁由 `ABD-032`、`ABD-038` 至 `ABD-040` 共同执行。

**格式**：`[P]` 表示在其依赖完成后可与其他 `[P]` task 并行，`US-*` 追溯到用户场景。实现所有权以 [`ARCHITECTURE.md`](ARCHITECTURE.md) 的能力领域映射为准；本清单和 issue record 只描述交付目标、依赖和验收。

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

## Phase 7: P2 受限写入能力

**目标**：在不扩张 provider 权限边界的前提下，逐项交付 P2 typed action handler。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-047 | ready | US-02 | 交付消息控制能力族：`message.send_location`、`message.send_custom` 和 `message.revoke`。详见 [`issues/ABD-047-message-controls.md`](issues/ABD-047-message-controls.md)。 | ABD-041, ABD-042 | 三个方法都有独立 schema、显式 grant target、operation/idempotency 和固定 SDK/server integration gate；位置和自定义 payload 受输入上限；撤回只允许 profile owner 在 allowlisted conversation 中发送的消息；未知结果一律不补发。 |

## Phase 8: P3 会话、关系与群组管理

**目标**：按可演示的 IM 能力族交付会话设置、好友/黑名单和群组管理；每个 issue 内的 typed method 共享授权边界和 integration gate。

| 完成 | ID | 状态 | 场景 | 任务与记录 | 依赖 | 完成条件 |
| --- | --- | --- | --- | --- | --- | --- |
| [ ] | ABD-048 | ready | US-02 | 交付媒体与文件消息能力族：`message.send_image`、`message.send_file`、`message.send_sound` 和 `message.send_video`。详见 [`issues/ABD-048-media-messages.md`](issues/ABD-048-media-messages.md)。 | ABD-046 | 方法只接受同 profile、同 run、未过期且类型匹配的 attachment reference；文件名、大小、时长和目标均受 grant 限制；不向 provider、日志或 control DB 暴露路径或内容；未知上传或发送结果不补发，且 integration gate 通过。 |
| [ ] | ABD-049 | ready | US-02 | `[P]` 交付会话设置能力族：`conversation.set_pinned` 和 `conversation.set_receive_option`。详见 [`issues/ABD-049-conversation-settings.md`](issues/ABD-049-conversation-settings.md)。 | ABD-041 | 显式 grant 只允许一个 conversation target；输入只允许 pinned boolean 或固定接收选项 enum；不公开通用 conversation patch，不触及 SDK local DB；每个方法有 operation/idempotency 和 integration gate。 |
| [ ] | ABD-050 | ready | US-02 | `[P]` 交付好友关系能力族：`friend.request`、`friend.respond`、`friend.delete` 和 `friend.set_remark`。详见 [`issues/ABD-050-friend-relationships.md`](issues/ABD-050-friend-relationships.md)。 | ABD-041 | 每个动作绑定 profile owner 和 allowlisted user；申请消息、response enum 与 remark 都受严格输入限制；response 和 delete 由固定 source 验证现有关系状态；每个方法 fail closed 并有 integration gate。 |
| [ ] | ABD-051 | ready | US-02 | `[P]` 交付黑名单管理能力族：`blacklist.add` 和 `blacklist.remove`。详见 [`issues/ABD-051-blacklist-management.md`](issues/ABD-051-blacklist-management.md)。 | ABD-041 | 每个动作只允许一个 allowlisted user，且绑定 profile owner；移除前由固定 source 验证现有关系状态；每个方法有 operation/idempotency、未知结果 fail closed 与 integration gate。 |
| [ ] | ABD-052 | ready | US-02 | `[P]` 交付群成员关系能力族：`group.join`、`group.leave`、`group.invite_members` 和 `group.remove_members`。详见 [`issues/ABD-052-group-membership.md`](issues/ABD-052-group-membership.md)。 | ABD-041 | grant 同时约束 group 和涉及的 user；申请消息与成员数量有受限输入；离开、邀请和移除均由固定 source/action 验证当前成员及操作者权限；每个方法 fail closed 并有 integration gate。 |
| [ ] | ABD-053 | ready | US-02 | 交付群管理能力族：`group.set_info`、`group.set_mute`、`group.set_member_mute` 和 `group.transfer_owner`。详见 [`issues/ABD-053-group-administration.md`](issues/ABD-053-group-administration.md)。 | ABD-041, ABD-052 | grant 同时约束 group 和成员 target；只公开有限群资料字段、mute boolean 与有上限的 member mute 时长；固定 source/action 验证管理或 owner 权限；每个方法有 operation/idempotency、未知结果 fail closed 与 integration gate。 |

**当前状态**：`ABD-024` 至 `ABD-032`、`ABD-035` 至 `ABD-046` 已完成 daemon、provider deployment boundary、全部 P1 typed server-read/action source 及 runtime/inbound、grant-bound message、action recovery、provider isolation、cancellation、privacy e2e gate，以及 P2 的 `message.send_text`、`message.send_quote`、`message.send_at`、`conversation.mark_read` 和附件基础设施。OpenIM 未公开 server unread count，故 `conversation.unread` 继续 `not_validated`。这些 action 只在 manifest、显式 grant 和对应 handler 共同允许时公开；引用额外要求会话和原消息 target，已读额外要求有限消息窗口。默认入站 policy 仍只授予 `message.history`。下一项是 `ABD-047` 的消息控制能力；P3 的媒体/文件、会话设置、好友/黑名单和群组写入已按能力族建立可追溯 issue。

## 执行顺序

`ABD-001` 完成后，Foundation 中标记 `[P]` 的 task 可并行；P0 checkpoint 后，事件账本和 grant/proxy 可按依赖并行，US-01 是首个可交付闭环。US-02 先交付 capability 执行面和首个 action handler，再由共享 typed read service 同时补齐 US-02 与 US-03；`ABD-024` 至 `ABD-032`、`ABD-035` 至 `ABD-046` 已完成 daemon、provider MCP、部署边界、全部 P1 server-read/action source 及 lifecycle/inbound/grant/action/isolation/cancellation/privacy e2e。`ABD-047` 不依赖附件基础设施；`ABD-048` 现在可在 `ABD-046` 的 attachment reference 上推进。P3 的 `ABD-049` 至 `ABD-052` 位于不同 IM 领域，可在 `ABD-041` 后并行；`ABD-053` 与群成员关系共享实现目录，在 `ABD-052` 的固定 action source 已确认后推进。
