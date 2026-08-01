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
| [`internal/capability`](../internal/capability) | capability manifest；action handler 与 daemon-owned action source 按 IM 领域组织（当前为 [`conversation`](../internal/capability/conversation)、[`friend`](../internal/capability/friend)、[`blacklist`](../internal/capability/blacklist)、[`group`](../internal/capability/group) 和 [`message`](../internal/capability/message)）。 |
| [`internal/service`](../internal/service) | owner/provider 共用的 typed read service contract 及各领域实现；group source 使用 daemon SDK context 调用服务端 API，不触及 SDK 本地数据库。 |
| [`internal/mcp`](../internal/mcp) | 基于 MCP `2026-07-28` 的 stdio JSON-RPC、owner daemon adapter 与 run-private provider adapter。 |
| [`docs/CONNECTOR.md`](CONNECTOR.md) | 外部部署 connector 的配置边界、启动顺序和 capability 验证门禁。 |
| [`.github/workflows`](../.github/workflows) 和 [`scripts/build-release.sh`](../scripts/build-release.sh) | PR/main CI、受控 OpenIM integration、tag 制品构建和 GitHub Release；不部署 daemon。 |

### P2/P3 Capability Ownership

下表是 P2/P3 能力的实现映射。状态为 planned 的行不表示 capability 已可用；每个公开方法仍必须经 manifest、grant 和固定 SDK/server integration gate。

| 能力族 | 状态 | Capability handler | Daemon-owned source | Tool/CLI registration | 验证 |
| --- | --- | --- | --- | --- | --- |
| 附件基础设施 | 已交付；不直接公开 provider 方法 | `internal/capability/message` | `internal/control`、`internal/profile` | 不直接公开 provider 方法 | `tests/e2e` |
| 消息控制 | delivered | `internal/capability/message` | `internal/bridge/abdim`、`internal/connector` | `internal/mcp/provider`、`cmd/abdim` | `internal/capability/message` unit + controlled integration |
| 媒体与文件 | delivered | `internal/capability/message` | `internal/bridge/abdim`、`internal/connector` | `internal/mcp/provider`、`cmd/abdim` | unit/proxy + controlled SDK/server integration |
| 会话设置 | delivered | `internal/capability/conversation` | `internal/connector` | `internal/mcp/provider`、`cmd/abdim` | `internal/capability/conversation` unit + controlled integration |
| 好友关系 | delivered | `internal/capability/friend` | `internal/connector` | `internal/mcp/provider`、`cmd/abdim` | `internal/capability/friend` unit + controlled integration |
| 黑名单管理 | delivered | `internal/capability/blacklist` | `internal/connector` | `internal/mcp/provider`、`cmd/abdim` | `internal/capability/blacklist` unit + fixed server source |
| 群成员关系 | delivered | `internal/capability/group` | `internal/connector` | `internal/mcp/provider`、`cmd/abdim` | unit/proxy + controlled server integration |
| 群管理 | delivered | `internal/capability/group` | `internal/connector` | `internal/mcp/provider`、`cmd/abdim` | unit/proxy + controlled server integration |

### P4 Ownership

| 能力族 | 状态 | 实现所有权 | 共享收口 | 验证 |
| --- | --- | --- | --- | --- |
| 多 provider | deferred | `internal/agent/provider`、`internal/launcher` | deployment provider registry、run construction | 暂不进入当前交付；保留单 Codex provider |
| 兼容矩阵 | delivered | `tests/compatibility`、capability evidence contract | daemon manifest construction | fixed SDK/server/provider matrix + controlled OpenIM probe |
| session migration | deferred | `internal/agent/provider`、`internal/agent/run` | session envelope/version registry | 依赖多 provider，暂不进入当前交付 |
| run operations | delivered | `internal/agent/run`、`internal/operation`、owner service | local RPC typed dispatcher | owner authorization/cancellation/privacy e2e |

## 当前实现状态

`daemon serve` 由单一 daemon 持有 SDK、控制库、owner socket、run manager 和固定 Codex App Server adapter。每个 run 都有独立 `CODEX_HOME`、MCP 配置、Unix bridge 和 grant；provider 只能发现 construction snapshot 中 manifest 与 grant 共同允许的 typed tools。`internal/launcher` 以部署指定的独立 UID/GID 运行 provider，拒绝文件和命令审批，并在取消时销毁进程组与 run 目录。

所有 P1 typed read 都经固定 server source 提供，不读取 SDK 本地数据库。写入面已包括群创建、成员关系和群资料/禁言/群主转让、文本/控制/媒体消息、会话设置、好友和黑名单；每项均经 method-scoped target、operation/idempotency guard 和未知结果 fail-closed 保护。媒体内容只在 profile 私有目录和 daemon 内 file handle 中流转，control DB 只保存不透明引用和约束 metadata。群成员和群管理动作以固定 server endpoint 验证角色和成员状态，不调用会同步本地状态的 SDK Group API。默认入站 policy 仍只授予 `message.history`；`conversation.unread` 因服务端未公开该值而保持 `not_validated`。

`available` 必须由固定 SDK/server/provider 组合的 integration gate 证明，不能由 manifest 静态声明替代。daemon 启动时将实际 MCP/SDK 组合与固定 evidence 精确匹配；未命中时 action manifest 自动降为 `not_validated`。run/operation 诊断只经 owner local service 暴露，provider tool registry 明确排除这些方法。

交付自动化将普通 CI、会产生 OpenIM 测试数据的受控 integration，以及 tag 制品发布分离。CI 仅使用无凭据测试和 fake provider；受控 integration 只从受保护 GitHub environment 读取短期 OpenIM token；tag workflow 只创建 GitHub Release，不拥有 daemon 主机或 deployment 凭据。具体配置和发布步骤见 [`RELEASING.md`](RELEASING.md)。

## 架构不变量

- 一个 profile 同时只能由一个 daemon 持有 SDK、控制库和运行时目录。
- provider 不能选择 reply conversation、调用任意 RPC/SDK 方法，或绕过 grant 的 method-scoped typed target 读取或写入数据；target 固定编码为 `conversation:<id>`、`group:<id>`、`message:<id>` 或 `user:<id>`，同一原始 ID 不可跨资源类型使用。
- 同一入站 event 只有一个账本记录和一个 reply slot；同一 conversation 的 provider turn 串行。
- 所有远端副作用都以 scope 和 idempotency key 绑定 operation；`unknown` 是终态，需要查询而不是新建请求。
- capability 只有同时被 manifest 和 grant 允许时才可供 provider 使用；owner 也只能经 typed 服务访问公开能力。
