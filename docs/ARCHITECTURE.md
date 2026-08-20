# abdim 架构

本文描述当前生产路径。daemon 是长期在线的 IM 客户端，不是 task daemon；消息到达后启动
一个 Agent turn，并把最终文本自动送回原会话。

## 组件

```text
                         abdim daemon
  +--------------------------------------------------------+
  | bot SDK  -> direct message / hosted notification       |
  | user SDK -> owner history and hosted reply transport   |
  |                                                        |
  | event dedup -> identity:conversation queue -> Agent    |
  | local CLI socket -> selected user or bot SDK services  |
  +----------------------------+---------------------------+
                               |
                               v
                    Codex app-server / ACP Agent
                    workspace + static abd-im Skill
```

一个 profile 只有一个 daemon 进程锁和一个本地 CLI socket，但包含两个完全独立的 SDK
context、token 和数据目录：

```text
<profile-data>/sdk/user
<profile-data>/sdk/bot
```

bot SDK 安装消息与 business listeners；user SDK 的普通消息 callback 不触发 Agent，避免绕过
服务端 hosting 配置或与 business notification 重复触发。

## 入站路径

direct 路径：

```text
bot private message
  -> persist dedup reference
  -> queue key bot:<conversation_id>
  -> prompt says Reply mode: direct and --as bot
  -> Agent final text
  -> bot SDK sends to original sender
```

hosted 路径：

```text
openim-chat confirms hosting
  -> secretary.business_message notification to bot SDK
  -> validate owner_user_id against local user SDK account
  -> user SDK loads trigger and recent history
  -> queue key user:<conversation_id>
  -> prompt says Agent represents owner and should use --as user
  -> Agent final text
  -> user SDK sends to the original conversation target
```

服务端通知只携带稳定引用和 instruction，不加载历史，也不代发回复。daemon 从本地 user SDK
获取 owner 可见的数据。自动回复目标保存在处理当前事件的内存值中；没有 Reply Slot、task 或
通用 operation ledger。daemon 崩溃时不恢复旧 turn，也不重发结果不确定的回复。

## 调度与 Session

Run Manager 只保留即时通讯需要的控制：

- 同一 `identity:conversation_id` 串行；
- profile 内最多并行两个 Agent turn；
- 每个 conversation 最多等待两个 turn；
- turn deadline、取消和 daemon shutdown；
- conversation 级 provider session resume。

每个 turn 启动独立 Agent 进程和 workdir。Codex 的 conversation state 以不可逆 state key
隔离，`control.db` 保存 `(profile, identity:conversation, provider) -> session_ref`。新的消息可
恢复 conversation 上下文，但不会恢复崩溃前正在执行的 turn。

## CLI 与权限

人和 Agent 使用同一个普通 CLI：

```text
abdim --as user <domain> <command>
abdim --as bot <domain> <command>
```

CLI 将固定命令映射到 daemon 方法，dispatcher 再选择 user 或 bot SDK service。CLI 不读取
token，不装配权限，也不生成动态命令目录。服务端按所选 SDK token 校验真实权限。

Codex workdir 包含随当前 `abdim` 版本发布的 `.agents/skills/abd-im`。Skill 说明身份选择、
命令参数和失败处理；运行时 prompt 单独说明当前 turn 是 direct 还是 hosted。首期不实现分层
`--help` 能力发现，该方向只保留在重构方案中继续探索。

## 持久化

`control.db` 只保存：

| 数据 | 用途 |
| --- | --- |
| profile ID | 本地数据库归属 |
| inbound event reference | callback 去重 |
| provider session reference | 后续消息恢复 Agent conversation |

数据库不保存 token、完整消息正文、prompt 或 Agent 输出。版本 8 migration 初始化当前最小表；
升级已有数据库不会主动删除历史旧表，但生产代码不再读写它们。

## 代码导航

| 路径 | 职责 |
| --- | --- |
| `cmd/abdim` | setup、daemon 生命周期、CLI 和 composition root |
| `internal/profile` | 双身份 profile、凭据引用和本地路径 |
| `internal/bridge/abdim` | 一个 OpenIM SDK context 的生命周期、事件与文本发送 |
| `internal/daemon` | 双 SDK runtime、inbound、身份 dispatcher |
| `internal/agent/run` | conversation 队列、并发、deadline、session resume |
| `internal/agent/provider` | Codex app-server 与固定 ACP adapters |
| `internal/service` | SDK-backed CLI 读取服务 |
| `internal/control` | 去重和 provider session 的最小数据库 |
| `skills/abd-im` | 随二进制发布的静态 Agent Skill |
