# abdim 架构

## 当前结构

```text
OpenIM callback
    -> event ledger
    -> inbound policy
    -> per-conversation run queue
    -> Codex app-server / ACP Agent
    -> event-bound Stream reply

Agent
    -> run-private abdim CLI
    -> run-private Unix socket
    -> typed IM handlers
    -> daemon-owned OpenIM SDK/server client
```

daemon 是唯一持有 OpenIM SDK、控制数据库和 owner socket 的进程。CLI 和
Agent 都不直接打开 SDK 数据库。

## Provider

- `codex` 直接启动 `codex app-server`。
- `hermes` 和 `openclaw` 保留固定 ACP v1 启动入口，不支持任意 provider 命令。
- 每个 run 都有独立工作目录、私有 socket、短期 grant 和允许方法快照。
- Agent 只能通过注入的 `abdim` CLI 调用 IM 方法；owner 管理方法不会出现在
  run 命令列表中。

Codex app-server 使用 `approvalPolicy=never` 和
`sandbox=danger-full-access`，因此命令、文件和网络工具不会被 adapter 全部禁用。
permission 请求只回传 Codex 请求中的文件系统和网络范围。这个模式信任当前
本机用户，不是同 UID 恶意进程的操作系统隔离。

## 会话

OpenIM `conversation_id` 是会话隔离键：

- event、reply slot 和 run 都记录 conversation ID。
- 同一 conversation 的 run 排队执行，不与其他 conversation 混用队列。
- 回复目标只来自持久化 reply slot，Agent 不能改写目标。
- run 及其状态持久化，可供未来网页工作区列出、取消和展示历史状态。

每个 run 仍使用自己的 provider 进程、grant 和工具代理，但 SQLite 按 profile 和
conversation 和 provider 保存 session ID。后续 run 恢复同一 session；provider 明确报告
session 不存在时，daemon 删除旧映射并创建新 session。不同 conversation 不共享上下文。

## 简单权限

权限只保留运行所需字段：

- `run_id`、`profile_id` 和调用者 principal。
- 允许的 typed method 列表。
- 过期时间和调用预算。
- 当前 conversation 的消息读取窗口。
- 附件字节上限。

默认入站 run 的 method 列表为空，但仍可回复。执行
`abdim inbound tools enable` 后，run 获得固定 registry 中可用的 IM 方法。
proxy 每次调用只校验 credential、run、profile、method、有效期和预算；handler
继续负责参数校验、消息窗口、IM 状态和副作用幂等。

## 持久化

`control.db` 只保存运行控制数据：

| 实体 | 用途 |
| --- | --- |
| `Event` | 入站去重、顺序和 conversation/message 标识。 |
| `ReplySlot` | 把 run 回复固定到触发会话。 |
| `Run` | conversation、event、状态和有限错误原因。 |
| `Operation` | 写操作幂等状态：`confirmed`、`failed` 或 `unknown`。 |
| `Attachment` | run 范围的不透明附件引用和额度。 |

grant 只存在于 daemon 内存中，daemon 重启后不恢复旧 grant。旧数据库 migration 中
保留已废弃的 `grants` 表，避免破坏已有 profile 数据库；生产代码不再读写它。

## 代码位置

| 路径 | 职责 |
| --- | --- |
| `cmd/abdim` | setup、daemon 生命周期、owner/run CLI。 |
| `internal/agent/provider/codex` | Codex app-server adapter。 |
| `internal/agent/provider/acp` | 其他 Agent 的 ACP v1 adapter。 |
| `internal/agent/run` | conversation 队列、deadline 和取消。 |
| `internal/agent/grant` | 内存 grant。 |
| `internal/agent/access` | run 私有 socket 和 CLI 环境。 |
| `internal/agent/proxy` | typed method 调用边界。 |
| `internal/daemon` | 入站编排和 owner RPC。 |
| `internal/control` | event、reply、run、operation 和 attachment 持久化。 |
| `internal/commands` | owner/run CLI 的固定命令 schema。 |
| `internal/service` | daemon-owned typed 读取服务。 |
| `internal/capability` | IM 写操作 handlers。 |

## 不变量

- 一个 profile 同时只运行一个 daemon。
- 一条入站 event 只创建一个 reply slot；回复不能跨 conversation。
- provider 不能调用 owner 管理方法或未授予的 IM method。
- 消息历史不能越过 run 的 conversation 和消息窗口。
- 远端写操作不自动重试 `unknown` 结果。
- 控制数据库和日志不保存 token、完整 prompt 或 Agent 输出。
