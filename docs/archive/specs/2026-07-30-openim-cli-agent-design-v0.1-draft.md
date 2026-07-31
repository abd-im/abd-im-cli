# OpenIM CLI 面向 Agent 的设计方案（已归档）

> 归档日期：2026-07-31  
> 原文件最后修改：2026-07-30  
> 原路径：`multica/doc/blog/openim-cli-agent-design.md`  
> 状态：已废弃，仅保留历史记录，不得作为实现依据。  
> 当前唯一规范：[`../../spec.md`](../../spec.md)

> 原始状态：Draft  
> 目标仓库：`openim-sdk-core`  
> 项目名：OpenIM CLI；下文暂定发布二进制名为 `openim`。若发行渠道已有名称冲突，可保留项目名并改用 `openim-cli`。

## 1. 背景与结论

OpenIM SDK Core 已经提供了完整的本地 IM 客户端能力：长连接、断线重连、消息补偿、本地 SQLite 数据库，以及会话、消息、群组、好友、黑名单和用户资料等 API。React Native demo 在此基础上实现了会话列表、历史消息、文本和媒体消息发送、群管理、联系人管理、未读数、在线状态和 SDK 事件处理。

本方案为这些能力增加一个可被 Agent 稳定调用的本地入口，而不是再实现一套 HTTP 客户端：

```text
Codex / Claude Code / Hermes-agent / 任意脚本
       | CLI 或 MCP stdio
       v
openim CLI client / MCP bridge
       | 本机 Unix socket（Windows 为 named pipe）
       v
openim daemon（每个 profile 一个进程）
       | SDK facade + callbacks
       v
open_im_sdk -> OpenIM Server / 本地 SQLite
```

### 对 Multica 的取舍

`multica-local-client-study.md` 很有参考价值，特别是单一二进制、daemon 生命周期、健康检查、最小权限、持久化事件、结构化日志和可恢复的本地状态。这些原则应直接采用。

但 OpenIM CLI 不应在第一阶段照搬 Multica 的“服务端任务队列 -> daemon claim -> provider 子进程”模型：OpenIM 的事实来源是 SDK 的长连接、同步器和本地消息库，CLI 的主要角色是让已经运行的 Agent 使用 IM 能力，而不是调度或托管 Codex/Claude 进程。第一阶段也不做 provider 探测、Agent 进程管理、worktree 或远程任务领取。

若后续要实现“收到一条 OpenIM 消息后自动唤起 Agent 并回复”的 bot worker，再单独在 daemon 上增加 provider adapter。那一层可复用 Multica 的 `Backend / Session / Message` 抽象、任务专属状态目录、取消和 session 恢复；它不能污染面向所有 Agent 的基础 CLI。

## 2. 目标、范围与非目标

### 目标

- 让 Agent 以机器可读、可分页、可重试的方式读取 OpenIM 数据并执行已授权操作。
- 覆盖 React Native demo 所使用的绝大多数 SDK 能力，并以稳定的资源命令而非 SDK 函数名暴露。
- 保持 SDK 的长连接、同步、本地数据库和事件回调只有 daemon 一个拥有者。
- 同时提供 shell CLI 和 MCP stdio bridge；前者适合 `exec`，后者适合原生工具调用。
- 支持多个 OpenIM 服务/账号 profile，且彼此的数据库、凭据、事件和权限不混用。
- 对发消息、群管理、删除本地数据等副作用操作默认收紧，并提供审计、幂等和明确失败状态。

### 第一阶段不做

- 不把 OpenIM CLI 做成远程管理面或对公网开放的 HTTP 服务。
- 不直接支持 React Native demo 的业务 HTTP 接口，例如 `/account/login`、手机号验证码、组织用户搜索和业务资料更新。这些 API 由具体部署决定，不属于 SDK Core；后续可通过 deployment connector 插件接入。
- 不把摄像头、麦克风采集、移动推送、UI 导航等设备/UI 行为伪装成 Agent 能力。Agent 可以发送已有的图片、声音、视频和文件。
- 不提供一个可执行任意 SDK 方法的 `sdk.call`/`raw-rpc` 后门。它会绕过参数校验、权限模型、审计和兼容性承诺。

## 3. 核心设计决定

### 3.1 单一二进制，daemon 是子命令

用户安装一个 `openim`，而非 `openim` 与 `openimd` 两个独立产品：

```text
openim profile create ...
openim auth login ...
openim daemon start --profile work
openim daemon status --profile work
openim conversation list --profile work
openim mcp serve --profile work --grant-file ...
```

`daemon start` 的后台模式由当前二进制重新执行 `daemon start --foreground` 实现。后台父进程只在本地健康端点确认 `ready` 后返回；PID 存在不是可用的判断依据。

### 3.2 一个 daemon 只拥有一个 profile

`open_im_sdk.IMUserContext` 是全局单例，并拥有登录身份、长连接、listener 与本地数据库。因此一个 daemon 进程只服务一个 profile，不能在同一进程切换多个账号。一个 profile 包含：

- API 地址、WebSocket 地址、平台标识、用户 ID 和 SDK 数据目录；
- 与该身份关联的凭据和 agent grant；
- 该 profile 的 IPC socket、PID、日志、事件序号和幂等记录。

多账号通过多个 daemon 实现，例如 `work`、`bot-dev`。这样既与 SDK 现状一致，也避免登录切换时把一个账号的本地消息暴露给另一个账号。

### 3.3 CLI、MCP 和 daemon 只保留一套领域 API

`openim conversation list`、`openim mcp serve` 和后续 bot worker 都调用 daemon 的同一套版本化领域方法，例如 `conversation.list`、`message.send`、`group.member.list`。MCP 不是第二套业务实现，只是 stdio 协议适配器。

CLI 默认输出 JSON；stdout 只输出结果，日志和进度只写 stderr。这样 Agent 不需要从人类表格或混杂日志中提取数据。

### 3.4 SDK 仍是状态所有者

daemon 只能通过 SDK facade 读取/写入数据、发送消息和注册 listener，不得直接查询或修改 OpenIM 的 SQLite 表。CLI 自己也不得 `InitSDK` 或打开 SDK 数据库。

这保留了 `LongConnMgr`、`MsgSyncer` 和 `Conversation` 已有的消息补偿、去重与会话更新语义。daemon 仅增加请求编排、事件规范化、授权、幂等和本机 IPC。

## 4. 架构

```text
                              +---------------------------+
                              | Agent host                |
                              | Codex / Claude / Hermes   |
                              +-------------+-------------+
                                            | exec 或 MCP
                 +--------------------------+--------------------------+
                 |                                                     |
        +--------v--------+                                   +--------v--------+
        | CLI command     |                                   | MCP stdio bridge |
        | args / JSON I/O |                                   | typed tools only |
        +--------+--------+                                   +--------+--------+
                 |                                                     |
                 +------------------- local IPC -----------------------+
                                     Unix socket / named pipe
                                               |
        +--------------------------------------v---------------------------------------+
        | openim daemon (one profile)                                                  |
        |  IPC auth | policy | service | idempotency | event hub | health | audit       |
        +---------------------------+--------------------------+------------------------+
                                    |                          |
                        SDK facade |                          | normalized events
                                    v                          v
        +-------------------------------+       +-------------------------------+
        | open_im_sdk                   |       | local daemon state             |
        | Init/Login/listeners/API       |       | grants, cursors, idempotency   |
        +---------------+---------------+       +-------------------------------+
                        |
        +---------------v-------------------+
        | OpenIM Server + SDK local SQLite   |
        +------------------------------------+
```

建议的代码边界如下，名称是实现建议，不是当前代码：

```text
cmd/openim/                         CLI root 与 daemon 子命令
internal/openimcli/daemon/          生命周期、健康、锁、重连状态
internal/openimcli/service/         conversation/message/group/friend 等领域服务
internal/openimcli/ipc/             本地协议、Unix socket / named pipe
internal/openimcli/mcp/             MCP stdio <-> daemon RPC adapter
internal/openimcli/policy/          grant、目标限制、批准与审计
internal/openimcli/event/           SDK callback 归一化、序号与订阅
internal/openimcli/sdkbridge/       callback-awaiting facade，不直接访问 DB
```

`sdkbridge` 使用公开的 `open_im_sdk` API 与 callback 接口把异步结果转换为带 `context.Context` 的 daemon 调用。消息创建和发送使用同一 request 的 `operationID`；全局 listener 仅在 daemon 启动时注册一次。

## 5. profile、启动与健康状态

### 5.1 本地文件布局

默认根目录为平台对应的应用数据目录；以下以 `~/.openim-cli` 表示：

```text
~/.openim-cli/
  profiles/<name>/
    profile.toml             非敏感连接配置，0600
    state/                   SDK dataDir、幂等记录、事件游标，0700
    run/daemon.sock          本机 IPC socket，目录 0700
    logs/                    结构化轮转日志
    grants/                  已加密或摘要化的 agent grant 元数据
```

Token 优先存入系统 keychain。没有可用 keychain 时，只有显式允许的情况下才使用权限为 `0600` 的本地凭据文件；daemon 在文件所有者或权限不正确时拒绝启动。Token、消息正文和附件路径不得出现在命令行、健康响应或默认日志中。

初始化与登录只接受安全输入：

```bash
openim profile create work --api https://im.example.com --ws wss://im.example.com --platform-id 5
openim auth login --profile work --user-id u_123 --token-stdin
openim daemon start --profile work
```

`--token` 这样的 argv 参数不提供。令牌刷新也不假设某个业务登录 API；部署方可更新 token 后调用 `auth login`，或在后续实现单独的 deployment connector。

### 5.2 启动顺序

```text
acquire profile lock
  -> bind local IPC and expose health=starting
  -> load profile, credential and policy
  -> InitSDK and register all listeners
  -> Login
  -> wait for connection and initial sync completion
  -> replay persisted daemon events if needed
  -> health=ready and accept normal requests
```

状态至少区分 `stopped`、`starting`、`authenticating`、`syncing`、`ready`、`degraded`、`locked`、`failed`。SDK 重连期间进入 `degraded`：本地缓存读取可返回 `stale: true`，需要服务端确认的写操作按策略排队或返回 `CONNECTION_UNAVAILABLE`，绝不假装已经发送成功。

`openim daemon status --output json` 返回版本、PID、profile、SDK 登录状态、同步状态、最后成功连接时间、未处理事件数和不含正文的诊断信息。`doctor` 检查配置、凭据可读性、socket 权限、SDK 数据目录和连接性。

## 6. 机器接口约定

### 6.1 通用输入和输出

所有命令支持：

- `--profile <name>`：默认 profile 必须显式配置，Agent 调用建议始终传入。
- `--output json|jsonl|table`：默认 `json`；`table` 只服务人工终端。
- `--request-id <uuid>`：调用方可提供；未提供则 CLI 生成。
- `--idempotency-key <opaque>`：所有具备外部副作用的命令要求提供，MCP bridge 自动生成并保存。
- `--grant-file <path>`：Agent 调用必须提供的能力 grant；交互式 owner 命令使用单独的本机解锁会话，不能由 agent grant 提权。
- `--input <file|->`：复杂结构化参数从 JSON 读取，避免把正文和数组拼进 shell 参数。

stdout 的单次结果使用稳定信封，成功或失败都可解析：

```json
{
  "apiVersion": "openim-cli/v1",
  "requestID": "01J...",
  "ok": true,
  "data": {},
  "meta": {"profile": "work", "stale": false}
}
```

失败时保持同一形状并使用非零退出码：

```json
{
  "apiVersion": "openim-cli/v1",
  "requestID": "01J...",
  "ok": false,
  "error": {
    "code": "POLICY_DENIED",
    "message": "The grant does not allow message.send to group g_42",
    "retryable": false
  }
}
```

列表统一返回 `items` 与不透明 `nextCursor`。CLI 可以在 daemon 内部转换 SDK 的 offset/page 参数，但不得把可变的本地 offset 作为长期恢复契约。时间同时保留 SDK 原始时间戳和 RFC 3339 表示；消息 ID、conversation ID、group ID 和 user ID 始终为字符串。

### 6.2 命令面

下面是稳定的资源命令，而不是逐字映射 Go 函数。每个命令有对应的 daemon RPC 和 MCP tool。

| 域 | 读操作 | 已授权写操作 |
| --- | --- | --- |
| profile / daemon | `profile list`、`daemon status`、`doctor` | `profile create`、`auth login`、`daemon start/stop` |
| user / online | `user me`、`user get`、`user presence` | `user update-self`、`presence subscribe/unsubscribe` |
| conversation | `conversation list/get/search/unread` | `conversation pin`、`mute`、`hide`、`draft set`、`private-chat set`、`burn-duration set`、`mark-read` |
| message | `message list/get/search`、`message status` | `message send`、`revoke`、`typing set`、`local-delete`、`delete` |
| group | `group list/get/search`、`group members list/search`、`group applications list` | `group create/join/leave`、`invite`、`kick`、`mute`、`member-mute`、`owner transfer`、`update`、`application accept/reject` |
| friend / blacklist | `friend list/get/search/applications`、`blacklist list` | `friend request/accept/reject/update/delete`、`blacklist add/remove` |
| events | `events watch`、`events cursor` | 无；订阅本身不改变远端状态 |

典型 Agent 调用如下：

```bash
openim conversation list --profile work --grant-file /run/openim/grant --limit 50
openim message list --profile work --grant-file /run/openim/grant --conversation cv_123 --limit 20
openim message send --profile work --grant-file /run/openim/grant --to group:g_42 --text "构建已完成" --idempotency-key task-918-reply-1
openim group members list --profile work --grant-file /run/openim/grant --group g_42 --limit 100
openim events watch --profile work --grant-file /run/openim/grant --types message.received,group.member.changed --cursor evt_01J...
```

`message send` 支持 `text`、`advanced-text`、`at`、`quote`、`image`、`sound`、`video`、`file`、`location`、`card`、`custom`、`merge`、`forward` 和 `face` 等 SDK 消息类型。媒体类命令接收受允许路径中的现有文件，daemon 使用 SDK 的 `Create*Message*` + `SendMessage` 流程创建和发送，上传进度通过 `jsonl` 或 MCP progress 事件返回。

### 6.3 与 React Native demo 的能力对应

| React Native demo / SDK 能力 | CLI 资源 | 首发阶段 |
| --- | --- | --- |
| SDK 初始化、登录、同步、登出、连接事件 | `profile`、`auth`、`daemon`、`events` | P1 |
| 会话列表、会话详情、未读数、置顶/免打扰/私聊/阅后即焚 | `conversation` | P1 读取，P2 写入 |
| 历史消息、本地搜索、已读、撤回、删除、输入状态 | `message list/search/...` | P1 读取，P2 写入 |
| 文本、图片、文件、声音、视频、引用、@、自定义等消息 | `message send` | P2，按消息类型逐个验收 |
| 已加入群、成员、群申请、创建/加入/邀请/踢人/禁言/转让 | `group` | P1 读取，P2/P3 管理 |
| 好友、好友申请、备注、黑名单 | `friend`、`blacklist` | P1 读取，P3 写入 |
| 用户资料和在线订阅 | `user`、`presence` | P2 |
| React Native 业务登录、验证码、业务用户搜索 | deployment connector | 不属于 SDK CLI 核心 |
| 推送、相机、设备音频、UI 展示 | 不提供等价命令 | 不适用 |

“几乎所有 React Native 能做的功能”在这里意味着 SDK IM 能力的覆盖，而不是把手机 UI、部署方私有账号体系或设备能力硬塞进 CLI。

## 7. 事件、同步与并发

### 7.1 事件归一化和恢复

SDK 的 `OnConnListener`、`OnConversationListener`、`OnAdvancedMsgListener`、`OnGroupListener`、`OnFriendshipListener` 和 `OnUserListener` 在 daemon 内归一化为版本化事件：

```text
connection.changed
sync.started | sync.progress | sync.finished | sync.failed
message.received | message.revoked | message.deleted | message.read
conversation.created | conversation.changed | conversation.unread.changed
group.changed | group.member.changed | group.application.changed
friend.changed | friend.application.changed | blacklist.changed
user.changed | presence.changed
```

daemon 为事件分配 profile 内单调递增的 `sequence` 与 `eventID`，先写入小型本地事件账本，再推送给 `events watch` 和 MCP。断线重连的客户端以 cursor 恢复；事件账本采用容量和时间双重保留策略，cursor 过期时返回 `CURSOR_EXPIRED` 并要求调用方重新读取资源快照。

普通 IM 消息的正文不会因为事件桥接而额外写入 daemon 日志。事件账本只保存恢复所需的最小字段和对 SDK 本地数据的引用；读取消息内容仍经 `message get/list` 的权限检查。

### 7.2 发送幂等与未知结果

网络超时后“CLI 没有收到结果”不等于“消息没有送达”。对所有远端写操作，daemon 保存：`grant principal + method + idempotencyKey + 参数摘要 + operationID + clientMsgID + 终态`。

发送消息流程为：

```text
validate grant and target
  -> atomically reserve idempotency key
  -> create message with stable clientMsgID
  -> persist pending record
  -> SDK SendMessage
  -> persist success / failure and return result
```

相同 key 的重试返回已有终态或 `pending`，不会再次创建消息。崩溃或连接中断后，daemon 使用 `clientMsgID` 查询本地会话与 SDK 发送状态；无法确认时返回 `OUTCOME_UNKNOWN`，由 Agent 显式决定继续等待还是在用户允许下发送新消息。该设计是至少一次网络下的可审计去重，不宣称无法由服务端保证的全局 exactly-once。

针对同一 conversation/group 的改变操作按资源键串行化，避免两个 Agent 对同一草稿、会话设置或群成员状态产生本地竞争；不同会话仍可并发。

## 8. Agent 接入与授权

### 8.1 两种接入方式

1. 具备 shell 工具的 Agent 直接执行 `openim ... --output json --grant-file <path>`。
2. 支持 MCP 的 Agent 配置 `openim mcp serve --profile <name> --grant-file <path>`。该进程只处理 stdio，并转发到已运行 daemon；它不初始化 SDK、不持有长期 OpenIM token，也不监听网络端口。

MCP 暴露精确的 typed tools，例如 `openim_conversation_list`、`openim_message_list`、`openim_message_send`、`openim_group_members_list`。工具描述应包含目标、正文、附件和副作用等级。禁止提供一个“任意 CLI / 任意 RPC”工具，否则模型可绕开后续所有权限边界。

Codex、Claude 和 Hermes-agent 不需要专属 adapter 或本机二进制探测：只要能执行 CLI 或 MCP stdio，就使用相同协议。这是与 Multica provider adapter 最大的不同，因为这里外部 Agent 是调用者，不是 daemon 要托管的任务执行器。

### 8.2 grant 与最小权限

同一操作系统用户可访问 socket 只是基础边界，不足以把任意 Agent 与人工 CLI 区分开。agent 接入必须使用由用户创建的、可撤销且有期限的 grant：

```text
openim agent grant create \
  --profile work \
  --principal codex-project-a \
  --allow conversation.read,message.read,message.send \
  --conversation cv_123 --group g_42 \
  --expires 8h --max-sends 20
```

grant 至少包含 profile、principal、允许的方法、允许的 conversation/group/user 目标、文件根目录、过期时间、速率/数量上限和批准策略。daemon 校验 grant，而不是相信 Agent 在命令行中声称的身份。grant 文件传给 MCP bridge 或受控 Agent 环境，不能放进 prompt、日志或版本库。

grant 的安全边界必须如实定义：它能限制通过 daemon 的调用，但不能抵御一个拥有同一操作系统用户的任意 shell、文件系统和进程访问权的恶意 Agent。这种 Agent 可以读取同用户可读的 grant，甚至绕开 CLI 直接读取本地数据。对不完全信任的 Agent，必须使用受控 MCP tool host、独立 OS 用户或容器沙箱，并只向其注入该 Agent 的短期 grant；不得把 profile 数据目录、daemon socket 目录或 owner 解锁凭据挂载给它。

默认策略：

- 未授权 Agent 没有任何访问权。
- `*.read` 必须显式授予，因为聊天内容也是敏感数据。
- `message.send`、好友/群申请、群管理和用户资料变更必须显式授予，且可限制到目标会话。
- 删除远端消息、删除全部本地消息、解散群、转让群主、登出/清理 profile 属于高风险；默认不能授予自动执行，只能由交互式用户确认。
- 有外部副作用的工具返回可读的 action summary，供支持 tool approval 的 Agent host 展示。策略要求确认而 host 无法确认时，daemon 返回 `CONFIRMATION_REQUIRED`，而不是降级执行。

对于未来的“入站消息驱动 Agent”模式，reply target 必须由 daemon 从原始 `eventID` 恢复，不能让 provider 只凭 prompt 指定任意 `conversationID`。这避免提示注入把回复导向其他群或用户。

## 9. 本地 IPC 与安全

- Unix 使用 profile 私有目录内的 Unix domain socket，目录 `0700`、socket `0600`；Windows 使用具有限制 ACL 的 named pipe。默认绝不绑定 `0.0.0.0` 或局域网端口。
- daemon 校验 peer 的本机身份，并在请求层校验 grant。协议请求含 `apiVersion`、`requestID`、`method`、`params`、`grant` 和可选 `idempotencyKey`。
- daemon 和 MCP bridge 不将 OpenIM token 注入给 Agent；Agent 只拿自身 grant。grant 被撤销或过期后立即失效。
- `--file`、`--image`、`--video` 等参数由 daemon 做绝对路径、符号链接和允许根目录检查；MCP 不提供任意文件读取工具。
- 审计记录方法、principal、目标 ID、参数摘要、结果、requestID 和时间，不记录消息正文、token、附件内容或完整本地路径。详细内容调试必须是显式、短期、权限受限的开关。
- 配置解析、MCP 输入、SDK callback JSON 和事件 payload 都是非可信输入；所有字段在进入领域服务前做长度、枚举、ID 和分页限制校验。
- profile 的 SDK dataDir 只能由对应 daemon 使用，不能指向正在被 React Native、Desktop 或其他 daemon 运行中的数据目录。

## 10. 故障处理与运维

| 情况 | 预期行为 |
| --- | --- |
| daemon 已运行但未完成认证/同步 | 返回 `DAEMON_NOT_READY`；调用方可显式 `--wait`，不静默执行 |
| SDK 断线 | 状态为 `degraded`；缓存读标记 `stale`，写入按策略失败或保留 pending |
| token 过期/失效 | 进入 `locked`，停止向 Agent 暴露数据，等待 `auth login` 更新凭据 |
| daemon 崩溃重启 | SDK 重新同步；事件 cursor 与幂等记录从本地状态恢复 |
| 重复启动同一 profile | profile lock 阻止第二个 SDK/数据库拥有者，CLI 改连现有 socket |
| MCP bridge 退出 | daemon 继续运行；订阅可由 cursor 恢复 |
| 已授予的目标不再存在/权限变化 | SDK 返回真实错误，daemon 记录审计并不扩大 grant |

命令包括 `daemon logs`、`daemon status`、`doctor` 和 `daemon stop`。日志是结构化且轮转的；健康信息与 `doctor` 结果可被 Agent 解析，但默认经过脱敏。

## 11. 测试策略

测试不能默认连接真实 OpenIM 账号，也不能消费任何 Agent 的账号或配额。

- 单元测试：policy、grant 过期、目标限制、参数校验、JSON 信封、分页和错误码映射。
- service 测试：通过 fake `SDKFacade` 验证会话/群组/消息命令，以及 callback 到事件的归一化。
- 幂等测试：重复发送、超时后重试、daemon 崩溃后恢复、`OUTCOME_UNKNOWN`。
- IPC/MCP 测试：socket 权限、无 grant 拒绝、grant 撤销、stdio 协议 fixture、长事件流与 cursor 恢复。
- 集成测试：只在显式环境变量指向隔离 OpenIM 测试服务时执行，使用专用测试用户和临时 dataDir。
- 回归测试：以 React Native demo 使用的 SDK 方法建立 capability manifest；新增或删除公开能力时，测试要求更新 CLI 覆盖状态，而不是悄悄遗漏。

## 12. 分阶段交付

### P1：只读、生命周期与事件

- `profile`、`auth login`、`daemon start/status/stop/doctor`；单 profile 单 daemon。
- 会话、历史消息、群、群成员、好友、申请、黑名单、本人资料和未读数的只读命令。
- JSON 契约、Unix socket、健康状态、事件 cursor、只读 MCP tools、只读 grant。
- `sdkbridge` 与 fake SDK 测试基座。

验收：Agent 能安全地列出会话、读取指定会话的分页历史、查看群成员，并从断开的 `events watch` 使用 cursor 恢复，且未授权读请求被拒绝。

### P2：受控发送与常用会话操作

- 文本、@、引用、图片、文件、声音、视频、位置、卡片和自定义消息。
- 发送进度、稳定 clientMsgID、幂等表、发送状态查询。
- 已读、撤回、输入状态、草稿、置顶、免打扰、私聊和阅后即焚设置。
- 带目标限制和数量限制的 mutation grant，以及需确认策略。

验收：两个并发 Agent 使用同一个 idempotency key 时只产生一条消息；无权跨会话发送、无权路径读取和需要确认的操作均 fail closed。

### P3：完整关系与群组管理

- 创建/加入/退出群、邀请/踢人/禁言/转让/修改群资料、处理群申请。
- 好友申请、接受/拒绝、备注更新、删除和黑名单管理。
- 在线状态订阅、用户资料更新和更完整的消息类型/搜索过滤。

验收：每个公开的 SDK 领域能力在 capability manifest 中有 `supported`、`intentionally-not-exposed` 或 `deployment-extension` 三者之一，不能存在未说明的空白。

### P4：可选的入站 Agent worker

仅在确认产品需要自动回复时实施。以持久化入站事件、会话上下文快照、原始 event 绑定的 reply target、provider adapter、取消/恢复和任务专属凭据为基础；复用 Multica 的可靠性原则，但不改变 P1-P3 CLI/MCP 的调用模型。

## 13. 实施前需要确认的产品决定

本设计默认采用以下结论，可在开始编码前最终确认：

1. 首发二进制名采用 `openim`，并以单独的 `openim-cli` release 名称发布。
2. 首发只支持 Unix socket 与 Windows named pipe，不开放本地 TCP；远程管理另行设计。
3. SDK Core 保持通用性，业务账号登录/令牌刷新通过外部 token 或 deployment connector 解决。
4. P1 的 Agent 默认只读；发送能力从 P2 开始，且必须有明确 grant。
5. 自动触发 Codex/Claude/Hermes 回复不是 CLI P1 的隐式行为，而是 P4 的独立 bot worker 产品能力。

这些决定让 OpenIM CLI 能先成为可靠、可审计的 Agent IM 工具，同时为后续自动化 Agent 留出正确的 daemon、事件和权限边界。
