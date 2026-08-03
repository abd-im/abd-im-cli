# abdim-cli 产品规格

状态：当前规范

规范性：本文件是 `abdim-cli` 唯一长期产品规格，必须符合根目录 [`constitution.md`](../constitution.md)。历史材料只在 [`archive/`](archive/) 保留，不能作为实现依据。

| 项目 | 值 |
| --- | --- |
| 代码仓库 | `abd-im-cli` |
| 产品与本地协议 | `abdim-cli` |
| 二进制、daemon 子命令与 MCP 前缀 | `abdim` |
| SDK | `go.mod` 固定的 `github.com/abd-im/abd-im-sdk-core/v3` fork 修复版本 |

## 1. 用户场景与测试

### US-01：通过 IM 与 Agent 对话（优先级：关键）

用户完成一次 owner 配对后，在私聊或受支持的群聊中发消息。daemon 收到该 owner 的事件后唤起 provider；provider 返回 `final_text`，daemon 只向触发消息所在会话回复。普通对话不要求 provider 执行 `abdim` 命令。

**独立测试**：向一个允许触发的私聊注入一条消息，验证只创建一个 run、一个 reply slot 和一条原会话回复。

**验收场景**：

1. 给定 daemon 已 `ready`，当私聊消息命中 policy 时，系统创建 run 并回复原私聊。
2. 给定未配对或 sender 不是 owner，系统不创建 run；owner 在受支持的群聊中触发时也只回复该群。

### US-02：在受限授权下使用 IM 能力（优先级：关键）

用户可以要求 Agent 查询消息、用户、好友、群组或会话，或执行获准的 IM 副作用。provider 仅能通过 run 私有 tool proxy 调用 manifest 中的 typed `abdim` 方法；方法、读取窗口、目标、数量和批准条件均受 grant 约束。`group.create` 是首个写入示例，不是唯一或特殊的能力类别。

P1 的 Agent 可见读取能力包括 profile/user、conversation、message、friend/blacklist 和 group；每项只有在固定 SDK/server 集成测试将其标为 `available` 后，才可被 manifest 和 grant 发放。P1 以 `group.create` 验证通用远端副作用路径，后续写能力沿用同一规则加入。

**独立测试**：向 provider 发放受限读取和 `group.create` grant，验证允许的方法只返回目标和消息窗口内的数据；越权方法、目标和未知结果均不会造成额外副作用。

**验收场景**：

1. 给定只允许 `message.history` 的 grant，当 provider 查询时，系统只返回触发会话和消息窗口内的结果。
2. 给定有效成员 ID allowlist，当 provider 调用 `group.create` 时，系统只提交 allowlist 内的成员。
3. 给定超时或崩溃导致副作用结果未知时，系统只保留和查询原 operation，不用新 key 自动重试。

### US-03：Owner 查询和诊断本地 IM 状态（优先级：高）

owner 通过 CLI 或完全信任的本地 MCP 查询 profile、会话、消息、群、好友、黑名单、事件和 operation，并获取结构化健康与能力状态。

**独立测试**：在无真实账号的 fake SDK 环境中执行每个公开读命令，验证 JSON contract、分页/cursor 和错误码。

**验收场景**：

1. 给定同步缓存过期，当 owner 读取时，结果显式标记 `stale: true`，不伪造实时数据。
2. 给定 daemon 未就绪或凭据失效，当 owner 发起写入时，系统拒绝或保留 `pending`，不返回伪成功。

### 边界场景

- 重复回调、重启、撤回、权限变化、grant 过期和 owner 取消都不得产生补偿回复或第二个副作用。
- 连接/同步失败进入 `degraded`；凭据失效进入 `locked`；停机差异只能产生 `state.reconciled`。
- token、消息正文、附件内容和完整本地路径不得进入日志、审计或健康响应。

## 2. 范围、非目标与依赖

`abdim-cli` 为一个 OpenIM profile 提供本地 daemon、CLI、MCP 和入站 bot。`abdim setup` 通过固定 ABD 登录服务配置 bot、启动当前用户后台 daemon，并用一次性私聊消息绑定 owner。SDK 长连接、本地 SQLite、同步和 listener 只由 daemon 持有；CLI、MCP 和 provider 不直接初始化 SDK 或读取 SDK 数据表。首版只支持当前用户运行 daemon 并复用该用户已登录的本机 Codex CLI；这是受信任本地进程模型，不提供恶意代码的操作系统级隔离。

P1 交付 US-01、US-02 和 US-03 所需的单 profile 闭环、event-bound reply、受 grant 约束的 typed 读取能力，以及以 `group.create` 验证的通用副作用路径。通用 `message send`、附件和更多写能力从 P2 开始。

不提供公网 HTTP 管理面、任意 SDK/RPC/SQLite 后门、其他 IM 部署的通用登录配置、客户端 UI/设备能力、未经集成测试验证的 SDK 能力，或服务端全局 exactly-once 承诺。

## 3. 功能需求

### 运行时、凭据与存储

- **FR-001**：daemon 每进程只服务一个 profile，并独占 SDK data directory、token、日志和资源配额。
- **FR-002**：daemon 必须经 `NewLoginMgr` 创建 `UserContext`，严格按 `InitSDK -> InitResources -> 注册 listener -> Login` 初始化；包级 facade 不是公开路径。
- **FR-003**：`ready` 要求 IPC、连接和选定同步策略完成；连接或同步异常为 `degraded`，凭据失效为 `locked`。
- **FR-004**：`abdim setup` 必须以交互方式将 ABD bot 账号密码换取 canonical user ID 和 IM token；密码不得回显或持久化，token 只能存入 owner-only `0600` 文件，不得出现在 argv、profile TOML、默认日志、审计或健康响应。
- **FR-005**：控制库只保存 profile、事件/cursor、run/reply slot、operation/idempotency、grant、policy、审计和附件元数据；不得复制完整聊天库或消息正文。

### 本地接口与读取能力

- **FR-006**：本地接口只能使用 Unix socket 或 Windows 受限 ACL named pipe；P1 不监听 loopback TCP。
- **FR-007**：所有 RPC 必须使用 versioned JSON 信封、稳定错误码、`request_id` 和 `profile_id`；远端副作用还必须带 `idempotency_key`。
- **FR-008**：CLI 默认输出 JSON，watch 输出 JSONL，日志和进度只写 stderr；消息正文只能经 stdin 或文件传入。
- **FR-009**：P1 必须提供 profile/user、auth/daemon/doctor、会话、消息、群、好友、黑名单、event 和 operation 的 typed 只读查询，并返回分页/cursor、`stale` 和 capability 状态。owner 可调用其公开读集合；provider 只能调用 manifest 与 grant 共同允许的子集。

### 入站 bot 与 provider

- **FR-010**：首次启动必须生成 15 分钟有效的一次性 owner 配对码；只接受私聊中的精确配对消息并持久化 canonical sender ID。配对前的消息、非 owner 消息、自己发送的消息、通知和重复事件不得创建 run。
- **FR-011**：provider 之前必须持久化 reply slot；它绑定 event、profile、来源 conversation、触发消息、run 和 operation。provider 或 prompt 不得指定 reply `conversation_id`。
- **FR-012**：P1 的 provider 通过一个已配置 adapter 运行；provider 自行完成同步 tool loop，`TurnResult` 只返回 `final_text`、脱敏 tool 摘要、session reference 或诊断错误。
- **FR-013**：同一 conversation 的 run 必须串行；policy 必须定义每会话最大队列、turn deadline 和溢出行为。撤回、访问丢失、policy 失效、grant 过期或 owner 取消时，必须取消 run、关闭 proxy 并撤销 grant。

### 授权、写入与审计

- **FR-014**：grant 必须绑定 run、profile、principal、scope、按 typed method 隔离且带资源类型命名的目标 allowlist、消息窗口、附件额度、到期时间、速率预算和 approval policy。配对 owner 的自动 run 可获得全部当前 `available` 方法和宽目标，但仍必须使用具有效期、调用预算和附件额度的独立 grant。
- **FR-015**：受限 provider 只能连接每 run 私有的 tool proxy；proxy 必须逐请求验证一次性 credential、run、grant、到期时间、capability manifest、method allowlist、method-scoped target 和读取窗口，拒绝 controller 命令、endpoint 覆盖和未授权方法。
- **FR-016**：event-bound reply 由 daemon 执行而非通用 `message.send`；它以 `profile + event_id + reply_slot` 幂等。确认前中断为 `unknown`，不得创建新消息补发。
- **FR-017**：每个远端副作用都必须具有独立 scope、输入/目标 allowlist、数量上限、批准策略和 operation/idempotency。P1 以 `group.create` 作为首个经过验证的 handler：相同 key 返回原 operation，参数摘要不同返回 `IDEMPOTENCY_CONFLICT`，未知结果不得自动重建群。新 handler 只有在 manifest、授权规则和集成测试齐备后才能公开。
- **FR-018**：listener 回调只校验、复制和入队。事件账本使用 daemon `event_id`、profile sequence 和独立 SDK dedup key；SDK 重启不得伪造停机期间的 `message.received`。
- **FR-019**：每次 tool 调用和 reply 投递前都必须重验 event/reply slot、会话访问、grant、policy 和 allowlist；确认的 reply 永不重发。
- **FR-020**：审计只记录 actor、profile、grant、request/operation/event、方法、目标摘要、结果、耗时和重试次数。`abdim capabilities` 必须说明每项能力的状态、SDK/server 版本和原因。

## 4. 架构与接口契约

```text
OpenIM Server
    <-> SDK UserContext
    <-> abdim daemon (one profile per process)
            <-> local RPC <-> owner CLI / owner MCP
            <-> run tool proxy <-> provider CLI / provider MCP
```

目录约定：

```text
<config-dir>/abdim/profiles/<profile>.toml
<data-dir>/abdim/profiles/<profile>/{sdk,control.db,attachments,logs}/
<runtime-dir>/abdim/<profile>/{daemon.sock,daemon.lock,runs/}
```

Unix runtime 目录为 `0700`，socket 为 `0600`。daemon 退出时停止新请求和新 run，取消未完成工作后关闭 SDK 资源；重启不得用新消息掩盖未知结果。

请求必须包含 `api_version`、`request_id`、`profile_id`、`method` 和 `params`；受控 tool 调用加不透明 `grant`，远端副作用加 `idempotency_key`。响应始终带 `api_version`、`request_id` 和 `ok`；成功带 `data` 与 `meta.profile_id/stale`，失败带 `error.code`、`retryable` 和有限 `details`。最小错误码为 `INVALID_ARGUMENT`、`DAEMON_UNAVAILABLE`、`DAEMON_NOT_READY`、`PROTOCOL_UNSUPPORTED`、`AUTH_LOCKED`、`GRANT_INVALID`、`POLICY_DENIED`、`IDEMPOTENCY_CONFLICT`、`CONNECTION_UNAVAILABLE`、`SDK_ERROR`、`OUTCOME_UNKNOWN`、`CURSOR_EXPIRED`、`CONFIRMATION_REQUIRED` 和 `INTERNAL`。

```text
abdim [--profile NAME] [--output json|jsonl|table]
      [--timeout DURATION] [--request-id ID] <resource> <verb> [flags]
```

`abdim mcp serve` 是 owner 或完全信任本地 Agent 的 stdio 适配器，调用同一 daemon service interface。当前用户模式下，Codex 的 agent-mode 进程由 daemon 提供每 run 私有的 `CODEX_HOME`、当前用户非 MCP 模型/供应商配置、固定 MCP 配置和 run tool proxy；源 MCP 与 history 表不继承。相同 OS UID 不构成安全沙箱，因此本地部署只适用于信任该用户和其 Codex 的环境；正常 tool 调用仍必须经过 grant、typed proxy 和 event-bound reply。

```go
type Provider interface {
    Start(context.Context, StartRequest) (Session, error)
}

type Session interface {
    Turn(context.Context, TurnRequest) (TurnResult, error)
    Cancel(context.Context) error
    Close(context.Context) error
}
```

P1 只在同一 daemon 生命周期内复用 provider session；重启中的 turn 标记 `interrupted`，不自动恢复或重跑。公开生命周期命令只有 `setup`、`start`、`stop`、`restart` 和 `status`；后台装配入口不属于用户接口。

## 5. 关键实体与状态

| 实体 | 职责 |
| --- | --- |
| Profile | daemon 的唯一 SDK、凭据、目录和隔离边界。 |
| Event | 持久化的入站事实、顺序和 dedup 身份。 |
| Run | 由 event 触发的 provider turn 与其队列/取消状态。 |
| ReplySlot | event 到原会话回复目标的不可覆盖绑定。 |
| Grant | run 范围内可用的 scope、目标和额度。 |
| Capability | typed 方法的 schema、可用状态、所需 scope 和验证依据。 |
| Operation | 可查询的远端副作用与幂等身份，状态为 `confirmed`、`failed` 或 `unknown`。 |
| Policy | 触发、队列、deadline、审批和访问规则。 |

## 6. 能力与交付

`abdim capabilities --profile work --output json` 为每项能力返回 `available`、`gated`、`unsupported`、`server_required` 或 `not_validated`。能力状态以固定 SDK version 的 integration test 为准。

| 阶段 | 交付 | 退出条件 |
| --- | --- | --- |
| P0 | SDK 日志脱敏、v1 IPC/error/event schema、fake SDK/provider/proxy、生命周期 contract test | 测试日志无真实 token；无凭据环境可覆盖协议和生命周期。 |
| P1 | 一 profile daemon、event ledger、reply slot、单 provider bot、run proxy、profile/user/conversation/message/friend/blacklist/group typed 读取能力、capability manifest、首个 `group.create` handler、最小 operation/idempotency | 私聊仅向原会话回复一次；Agent 只可使用 grant 允许的能力；副作用未知结果 fail closed。 |
| P2 | `message.send_text`、`message.send_quote`、`message.send_at`、`conversation.mark_read`、受控附件基础设施和更多 verified action handler | 同幂等键只创建一个消息；超时或崩溃不静默产生第二条。 |
| P3 | 媒体/文件、会话设置、好友/黑名单和群组写入 | 每项公开能力都有 schema、scope、approval、integration test 和 capability 状态。 |
| P4 | 多 provider、兼容矩阵、session migration 和高级 run 运维 | 不改变 P1 的 reply target、调用模型或授权边界。 |

## 7. 成功标准

- **SC-001**：独立 `NewLoginMgr` worker 到达 `ready`，第二个同 profile daemon 因 lock 被拒绝。
- **SC-002**：同一入站事件只生成一个 ledger record 和一个 reply slot；重启差异只产生 `state.reconciled`。
- **SC-003**：普通私聊的 `final_text` 只回复触发会话，provider 没有执行发送命令。
- **SC-004**：provider 只能调用 manifest 与 grant 共同允许的读取方法，并且结果不超过其会话、目标和消息窗口。
- **SC-005**：`abdim group create` 只能创建成员 ID allowlist 内的群，并将结果返回同一 turn；任何已验证副作用在崩溃后只为 `confirmed`、`failed` 或 `unknown`，不会自动产生第二次副作用。
- **SC-006**：正常 provider 集成不能经 run-private MCP bridge 以外的路径直连 daemon、调用 controller 命令、读取超出消息窗口的历史，或向第三方会话发送；当前用户模式不声称能阻止同 UID 恶意进程直接访问本地文件或 socket。
- **SC-007**：撤回、权限变化、grant 过期或取消会阻止排队 run、副作用和最终回复。
- **SC-008**：daemon、SDK、HTTP/WebSocket 诊断和审计均不泄露测试 token 或完整消息正文。
- **SC-009**：一次 `abdim setup` 可完成 ABD 登录、私有配置和后台启动；配对前不创建 provider run，配对后只有持久化 owner 能触发全部已验证能力。

## 8. 假设

- 当前产品只连接固定 ABD 部署；用户拥有一个 bot 账号、一个独立 owner 账号，以及已登录的本机 Codex CLI。
- P1 固定一个经集成测试验证的 SDK/server 组合和一个 provider adapter。
- 需要额外能力或兼容组合时，先新增活跃 task，再改变 capability 状态或交付范围。
