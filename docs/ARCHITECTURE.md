# abdim-cli 架构

状态：实现架构说明

本文件说明 `abdim-cli` 的组件职责、信任边界和当前代码落点。产品范围、接口契约和交付要求以 [`spec.md`](spec.md) 为准；发生冲突时，以规格为准。本文件不将尚未组装的组件描述为已可用功能。

## 架构目标

一个 daemon 只拥有一个 OpenIM profile 的 SDK 生命周期、本地控制库和运行时资源。Owner 经本地 RPC 使用 daemon；受限 provider 只经一次 run 私有的 typed tool proxy 使用被授权的能力。CLI、MCP 和 provider 都不直接初始化 SDK 或读取 SDK 数据库。

```text
                        +----------------------+
                        |     OpenIM Server     |
                        +----------+-----------+
                                   |
                         SDK UserContext
                                   |
 +----------------------+----------+-----------+----------------------+
 |                    abdim daemon                                |
 |                                                                  |
 |  bridge -> event ledger -> reply slot -> run manager -> provider |
 |     |          |               |              |                  |
 |  SDK lifecycle  control.db   operation      grant -> tool proxy  |
 |     |          |               |                         |        |
 |  profile lock   +---------------+-------------------------+        |
 +----------------------+-------------------------------+------------+
                        |                               |
               Unix socket RPC                  run-private proxy
                        |                               |
              owner CLI / owner MCP             restricted provider
```

## 信任与所有权边界

| 边界 | 持有者 | 规则 |
| --- | --- | --- |
| SDK `UserContext`、SDK 数据目录和 listener | daemon | 只通过 `bridge.LoginMgr` 创建和关闭；同 profile 由 lock 排他。 |
| 控制面 SQLite | daemon | 仅保存控制元数据，不复制消息正文或 SDK 聊天库。 |
| 本地管理接口 | owner CLI / owner MCP | 通过 daemon 的本地 RPC 调用 typed service；不提供 TCP 管理面。 |
| provider 工具接口 | 单个 run | 仅暴露静态注册的 typed 方法，逐请求校验 grant、scope、目标和 capability。 |
| 远端副作用 | reply/action handler | 先记录 operation，再调用 SDK；不确定结果保持 `unknown`，不自动补发或重试。 |

受限 provider 不是 daemon 的同权限客户端。其运行环境不得挂载 daemon socket、profile、SDK 数据目录或 owner 凭据；相同 OS UID 不构成隔离边界。

## 运行时闭环

1. daemon 为 profile 创建私有目录和控制库，获取 profile lock，再按 `InitSDK -> InitResources -> SetEventListener -> Login` 启动 SDK。
2. listener 只复制回调身份并交给事件账本。账本以 `profile_id + SDK dedup key` 去重，分配 daemon sequence，并持久化 event。
3. policy 命中的 event 在 provider 开始前预留 `ReplySlot`。slot 固定 event、触发消息和来源 conversation，provider 无法覆盖回复目标。
4. daemon 创建具有效期和目标限制的 grant，并将只包含已注册方法的 proxy 交给 run manager。run manager 同一 conversation 串行执行，并在 deadline、撤回、策略变化或 grant 失效时取消 turn。
5. provider 返回 `final_text` 后，reply service 从持久化 slot 构造投递。它以 `profile + event_id + reply_slot` 创建 operation；已存在的 `confirmed`、`failed` 或 `unknown` operation 不会再次发送。
6. daemon 关闭时停止新请求和新 run，取消未完成 turn，关闭 provider session、SDK 和本地 socket。重启差异只能记录为 `state.reconciled`。

## 接口与数据

### 本地协议

本地 RPC 和 tool proxy 共用 [`internal/contracts`](../internal/contracts) 的 v1 JSON envelope。请求包含 `api_version`、`request_id`、`profile_id`、`method` 和 `params`；受控调用另带不透明 `grant`，远端副作用另带 `idempotency_key`。响应始终包含同一版本和请求 ID，成功响应包含 `meta.profile_id`，typed read 还会返回 `schema`、`stale` 和 capability 状态。

Unix 实现使用长度前缀帧和 owner-only Unix socket；Windows 的受限 ACL named pipe 是规格定义的对应实现。P1 不监听 loopback TCP。

### 持久化模型

`control.db` 中的关键实体是 `Profile`、`Event`、`ReplySlot`、`Grant` 和 `Operation`。其中 Event 只保存 conversation/message 标识和去重键；Operation 只保存 canonical 输入摘要。日志、审计和健康信息不得写入 token、消息正文、附件内容或完整本地路径。

目录由 [`internal/profile`](../internal/profile) 统一生成：

```text
<config-dir>/abdim/profiles/<profile>.toml
<data-dir>/abdim/profiles/<profile>/{sdk,control.db,attachments,logs}/
<runtime-dir>/abdim/<profile>/{daemon.sock,descriptor.json,daemon.lock}
<runtime-dir>/abdim-provider/<profile>/
```

目录权限为 `0700`，socket、profile 和显式明文凭据文件为 `0600`。凭据只保存不透明引用；明文文件存储必须显式启用。

## 代码映射

| 路径 | 职责 |
| --- | --- |
| [`cmd/abdim`](../cmd/abdim) | CLI 入口；当前实现 `auth import --token-stdin`。 |
| [`internal/profile`](../internal/profile) | profile 名称校验、私有路径、lock、凭据引用与显式文件 fallback。 |
| [`internal/bridge`](../internal/bridge) | 单 profile SDK 生命周期及状态转换。 |
| [`internal/contracts`](../internal/contracts) | v1 RPC/event/provider 的共享 Go contract。 |
| [`internal/ipc`](../internal/ipc) | Unix socket、长度前缀帧和请求/响应校验。 |
| [`internal/control`](../internal/control) | SQLite migration 与控制面实体持久化。 |
| [`internal/events`](../internal/events) | 回调去重、sequence、cursor、watch 和 reconciliation event。 |
| [`internal/reply`](../internal/reply) | reply slot 与 event-bound、幂等的最终回复。 |
| [`internal/daemon`](../internal/daemon) | 入站事件到 policy、run-private proxy、provider turn 和 event-bound reply 的生产编排；runtime 只在 SDK ready 后开放 owner-only socket。 |
| [`internal/agent/grant`](../internal/agent/grant) | run 级授权的发行、过期、目标和速率检查。 |
| [`internal/agent/proxy`](../internal/agent/proxy) | provider 的 run-private typed tool 边界。 |
| [`internal/agent/run`](../internal/agent/run) | per-conversation 排队、deadline、取消和 provider session 生命周期。 |
| [`internal/agent/provider/codex`](../internal/agent/provider/codex) | 固定 `codex app-server --listen stdio://` 的 JSON-RPC session、取消和审批拒绝。 |
| [`internal/capability`](../internal/capability) | capability manifest；action handler 与 daemon-owned action source 按 IM 领域组织（当前为 [`conversation`](../internal/capability/conversation)、[`group`](../internal/capability/group) 和 [`message`](../internal/capability/message)）。 |
| [`internal/service`](../internal/service) | owner/provider 共用的 typed read service contract 及各领域实现；group source 使用 daemon SDK context 调用服务端 API，不触及 SDK 本地数据库。 |
| [`internal/mcp`](../internal/mcp) | 基于 MCP `2026-07-28` 的 stdio JSON-RPC、owner daemon adapter 与 run-private provider adapter。 |
| [`docs/CONNECTOR.md`](CONNECTOR.md) | 外部部署 connector 的配置边界、启动顺序和 capability 验证门禁。 |

## 当前实现状态

基础模块和它们的单元测试已在仓库中存在，包括 profile/credential、SDK 生命周期边界、RPC framing、control store、事件账本、reply operation、grant/proxy、run manager、capability、typed service 和 MCP stdio adapter。`internal/daemon` 已组装入站 listener 到 reply 的生产路径；其 `Runtime` 在 SDK ready 后才开放 owner-only socket，并在启动失败和关闭时释放 inbound、SDK 和 profile lock。Dispatcher 的 `OwnerMethods` 逐项绑定 22 个当前 P1 typed-service read，并保留原 service 的 schema、stale 与 capability metadata。`internal/bridge/abdim` 已将 fork 的实例化 `UserContext` 映射为 daemon-owned lifecycle adapter，入站正文只以非序列化字段进入 provider prompt，控制库和 event 仍只保存身份引用；它使用 fork 的 context-bound text API 投递 callback 固化的 reply，并为 `message.send_text`、`message.send_quote` 和 `message.send_at` 提供窄 sender。CLI connector 现可通过 `daemon serve` 组装真实 SDK、owner socket 和固定 Codex App Server adapter。每个 Codex run 都启动新的 App Server session，并获得独立 `CODEX_HOME`、固定 stdio MCP 配置和单连接 Unix bridge；bridge 保留 grant 和 typed proxy，provider 只能发现构造快照中已验证且获授权的工具。`internal/launcher` 只接受 root-controlled 部署配置，并以独立 UID/GID 启动 provider；daemon 写完 run-private 配置后才交给该 UID，run parent 仍由 root 持有，provider 不能准备后续 run 路径。adapter 拒绝文件/命令审批，并在取消后销毁进程组和 run 目录。profile source 以 daemon-owned profile/runtime facts 与受控 `/user/get_users_info` source 提供 profile/self/user/daemon/doctor；group、conversation 的 `list/get/search`、message 的 `history/search/get` 及 social 的 friend/blacklist read 均已由受控 SDK/server integration gate 验证。`group.create` 通过 daemon-owned `/group/create_group` action source 接入 provider static registry，不调用会同步本地状态的 SDK API；`message.send_text`、`message.send_at` 和 `message.send_quote` 经同一 registry 调用 daemon-owned sender，其中引用先从固定 server-read history 重取并验证原消息。`conversation.mark_read` 先从固定 server-read source 解析消息 sequence，再调用固定 `/msg/mark_conversation_as_read` endpoint，不读取 SDK 本地数据库。所有 action 只有在 manifest 和 run grant 同时允许时才会暴露，默认入站 policy 不授予任一写入方法。conversation 未读数属于本地 SDK 状态，继续 `not_validated`；message 只使用经鉴权的 sequence server read，并在固定 100 条窗口内执行搜索、cursor 和 grant window；social 只调用当前 token owner 的 server endpoint，并在认证好友结果中搜索。默认 e2e gate 已验证 runtime lifecycle/profile lock、入站去重、event-bound reply 和 restart reconciliation。`abdim mcp serve` 已将 owner adapter 接至同一固定 registry。当前 CLI 提供 token 导入、profile 配置、daemon 校验和到本地 socket 的固定 owner 查询。`available` capability 的发布依据必须是固定 SDK/server 组合的集成测试，不能由 manifest 声明替代。

## 架构不变量

- 一个 profile 同时只能由一个 daemon 持有 SDK、控制库和运行时目录。
- provider 不能选择 reply conversation、调用任意 RPC/SDK 方法，或绕过 grant 的 method-scoped typed target 读取或写入数据；target 固定编码为 `conversation:<id>`、`group:<id>`、`message:<id>` 或 `user:<id>`，同一原始 ID 不可跨资源类型使用。
- 同一入站 event 只有一个账本记录和一个 reply slot；同一 conversation 的 provider turn 串行。
- 所有远端副作用都以 scope 和 idempotency key 绑定 operation；`unknown` 是终态，需要查询而不是新建请求。
- capability 只有同时被 manifest 和 grant 允许时才可供 provider 使用；owner 也只能经 typed 服务访问公开能力。
