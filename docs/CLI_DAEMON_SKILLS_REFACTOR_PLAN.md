# abdim daemon/CLI + Skill 重构方案

> 状态：已实施；分层 `--help` 留待后续探索
>
> 原则：直接、简单、可验证。daemon 保持即时通讯客户端形态，参考 Multica 的普通 CLI
> 使用方式和飞书 CLI 的 Skill、user/bot 双身份设计，不再构造 MCP 或 Run 私有工具系统。

## 1. 结论

目标架构只保留三个部分：

1. **daemon**：登录 IM，处理两类消息：发给 Agent 本人的消息，以及需要 Agent 代表用户回复的
   托管消息。
2. **CLI**：人和 Agent 共用的普通 IM 命令行客户端。
3. **Skill**：告诉 Agent 何时、如何使用 `abdim` CLI。

权限不再通过 CLI 动态组装。一个 daemon profile 本地登录 user 和 bot 两个隔离的 SDK context；
CLI 的 `--as` 只选择由哪个 SDK context 执行。OpenIM 服务端按对应 token 做标准鉴权，token 不暴露
给 Agent。

明确删除以下设计：

- MCP 和 Provider 专用 tool schema；
- 每 Run method snapshot；
- grant、调用预算和 Run-private socket；
- `ABDIM_AGENT_METHODS` 等工具注入环境变量；
- `abdim commands` 动态命令目录；
- `inbound tools enable|disable` 开关；
- 持久化 Reply Slot；
- 通用 operation/idempotency 子系统。

## 2. 双 SDK 与服务端托管边界

服务端继续保留托管控制面：

- owner 与 Agent/Bot 的绑定；
- conversation 级 `hostingEnabled` 和 instruction；
- 判断一条 owner 入站消息是否需要触发 Agent；
- 向当前绑定的 Agent 发送包含 owner、conversation 和 trigger 引用的 business notification。

服务端不再替 Agent 读取 owner 历史，也不再通过 `SendBusinessMessage` 代发回复。daemon 本地同时
登录两个独立 SDK context：

```text
daemon profile
├── bot SDK  -> Agent 自己的收发和 business notification
└── user SDK -> owner 的会话、历史、联系人，以及由 daemon 自动投递 hosted final text
```

当前 profile 只有一个 `CredentialRef`、一个 `Deployment.UserID` 和一个 SDK 数据目录，需改为 user
和 bot 两组 user ID、credential ref 与隔离的 SDK 数据目录。daemon 进程锁仍只有一个，进程内管理
两个 SDK 生命周期。

这意味着服务端托管记录控制“哪些消息触发 Agent”，user token 控制“`--as user` 能访问什么”。
如果继续保留 `historyAccessEnabled`，它只能作为托管行为配置下发；在没有额外代理或 scope 系统的
前提下，它不能限制完整 user SDK token 的实际访问范围。首期接受这一边界，不重新引入工具权限层。

CLI 中的命名统一为 `user` 和 `bot`：

- `--as user` 对应 owner 用户身份；
- `--as bot` 对应当前绑定的 Agent/Bot 身份。

这里的 `--as` 表示调用 API 时使用哪个凭据，不一定等于消息最终展示的发送者。Agent 必须知道
当前是在以自己身份回复，还是代表 owner 回复；但它不通过 `message send` 为当前入站消息选择
发送者和目标。

## 3. 目标架构

```text
直接消息 -> Agent 账号 -> abdim daemon -> Agent turn -> 以 Agent 身份回复

用户收到消息 -> openim-chat 确认已托管
             -> business notification -> bot SDK -> abdim daemon
             -> user SDK 读取 owner 上下文
             -> Agent turn（明确告知正在代表 owner）
             -> Agent 返回 final text
             -> daemon 通过 user SDK 自动发回原会话
```

两条链路共用 conversation 串行、Provider Session、Codex adapter 和 Skill。Agent 在 turn 中可以
通过普通 `abdim` CLI 获取信息或执行动作。

核心原则：**Agent 感知当前回复模式和自己代表的身份，但不能覆盖回复目标或传输层发送者。**

| 模式 | 入站来源 | Agent 对自己的理解 | 对方看到的发送者 |
| --- | --- | --- | --- |
| direct | 发给 Agent 账号的普通 IM 消息 | “我正在以 Agent 自己的身份回复” | Agent/Bot |
| hosted | 服务端托管通知，经 bot SDK 接收 | “我正在代表 owner 回复这个联系人” | user SDK 登录的 owner |

Agent 能按 owner 的立场和 conversation instruction 组织 hosted 回复。daemon 根据可信的托管通知
固定使用本地 user SDK 和原 conversation 自动投递 final text；Agent 不调用发送命令，也不能通过
输出修改 SDK 身份或目标。

## 4. daemon 设计

daemon 是长期在线的 IM 客户端，不引入 Multica 的 task、claim 或 complete/fail 模型：

```text
login bot SDK + user SDK
  -> bot SDK normal message triggers direct
  -> bot SDK business notification triggers hosted
  -> user SDK message callback does not trigger an Agent turn
  -> normalize and deduplicate inbound event
  -> direct uses bot context; hosted loads context through user SDK
  -> enqueue by conversation_id
  -> launch Agent turn
  -> collect final output
  -> daemon automatically replies through bot SDK or user SDK
```

一轮消息处理只在内存中携带最小入站上下文：

```text
reply_mode: direct | hosted
dedup_key
business_connection_id
conversation_id
trigger_message_id
owner identity
agent identity
counterpart identity
instruction
trigger message and locally loaded messages
```

`reply_mode` 不需要新增服务端状态：bot SDK 的普通 message callback 是 direct，现有
`secretary.business_message` notification 是 hosted。hosted notification 只负责触发和携带稳定
引用，daemon 使用 user SDK 读取 trigger 和所需历史。

user SDK 自己收到的普通 message callback 只用于 SDK 同步，不直接启动 Agent；否则会绕过服务端
`hostingEnabled`，并与 business notification 重复触发。conversation queue 和 Provider Session 的
key 使用 `identity + conversation_id`，避免 user/bot 两个 SDK 的会话相互污染。

现有 business notification 没有显式携带 `ownerUserID`，不能让 Agent 从聊天记录猜 owner。首期
只需补 `owner_user_id`，daemon 同时校验它与本地 user SDK 登录身份一致。Agent 身份来自 bot SDK，
counterpart 来自 user SDK 读取到的 trigger sender。

当前 `secretaryPrompt` 由服务端下发的历史拼接而成，同时没有说明 Agent 正在代表 owner。本次改为
daemon 通过 user SDK 读取上下文并构造 prompt；hosted prompt 至少要告诉 Agent：

- 当前是 hosted 模式，不是别人向 Agent 本人发消息；
- Agent 正在代表哪个 owner，正在回复哪个联系人；
- 最终消息会通过本地 user SDK 以 owner 身份发出；
- 访问托管用户的消息、联系人或其他资源时，使用 `abdim ... --as user`；
- 不要用 `abdim message send` 发送当前最终回复，只返回回复文本给 daemon；
- 应遵循 conversation instruction，并从 owner 的立场组织回复。

不需要为此生成长 prompt。运行时提示可以保持为：

```text
Reply mode: hosted. You are replying on behalf of <owner> to <counterpart>.
For abdim commands concerning the hosted user, use --as user.
Return the final reply text to the daemon; do not send it with abdim message send.
```

direct 模式对应提示使用 Agent 自身身份，涉及 Agent 账号的 CLI 调用默认 `--as bot`。具体命令、
flags 和安全规则仍从静态 Skill 按需读取，运行时 prompt 不重复整份 Skill。

### 不再保留 Reply Slot

daemon 的消息处理函数已经持有入站事件及其回复引用，并等待对应 Agent turn 结束。最终输出直接
使用这份内存上下文发送，不需要额外的 Reply Slot 表、预留流程和恢复逻辑。

daemon 崩溃时，当前 turn 随进程结束；不恢复旧 Turn，也不补发不确定的旧回复。后续新消息仍可
恢复同一 conversation 的 Provider Session。

### 最小状态

本地只保存 daemon 真正需要的状态：

- IM 登录信息；
- user/bot 两套隔离的 SDK 数据目录；
- 必要的入站消息去重键；
- 内存中的 conversation queue 和当前 turn；
- Provider Session ID；
- 有限错误信息。

业务通知的 `updateID` 只用于过滤重复回调，不代表额外的任务对象。首期不实现任务状态、通用
operation ledger 或自动补发；任一 SDK 发送结果不确定时不重试。

## 5. CLI 设计

CLI 首期命令面以当前 SDK 的真实能力为边界，不先设计命令再等待底层实现。
当前代码已经有 conversation、message、group、friend、blacklist 和 user/profile 等服务，可作为
首期盘点起点。服务端 API 只保留托管配置和触发通知，不承载普通 IM CLI 能力。

一个能力进入 CLI 和 Skill 前必须同时满足：

- 当前 SDK 已有可调用接口；
- `--as user|bot` 能明确路由到对应本地 SDK，OpenIM 服务端按该 token 鉴权；
- 可以提供稳定的 flags、JSON 输出和错误语义；
- 有对应的服务测试，关键写操作有端到端验证。

命令按 IM 领域组织，不直接暴露 SDK 方法名，也不追求包装 SDK 的全部接口。底层尚未支持或尚未
验证的能力不进入首期 CLI 和 Skill。下面的命令用于说明目标风格，最终首期清单以实现阶段的 SDK/
服务盘点结果为准：

业务 CLI 不再区分“人类模式”和“Agent Run 模式”。两者使用同一命令：

```bash
printf '%s\n' '{"limit":20}' | abdim --as user conversation list --params-stdin
printf '%s\n' '{"conversation_id":"<id>","limit":20}' | abdim --as user message history --params-stdin
printf '%s\n' '{"recipient_id":"<id>","text":"hello"}' | abdim --as bot message send --params-stdin
printf '%s\n' '{"group_id":"<id>","limit":20}' | abdim --as user group members list --params-stdin
```

`message send` 只用于明确的主动发消息操作，不用于回复当前 inbound turn。当前 turn 的 final text
始终由 daemon 使用入站引用自动投递。

职责保持简单：

- 解析 profile 和 `--as user|bot`；
- 通过本地 daemon 路由到对应 SDK context；
- 校验 flags；
- 调用现有 SDK 能力；
- 输出稳定 JSON；
- 原样表达 SDK/服务端权限错误。

CLI 和 Agent 都不读取 token，也不复制授权规则。命令可以说明推荐身份，最终结果以对应 SDK token
获得的服务端权限为准。

本地生命周期命令单独放在 `daemon` 下：

```bash
abdim daemon start
abdim daemon stop
abdim daemon restart
abdim daemon status
abdim daemon logs
```

## 6. Agent 如何理解 CLI

Multica 没有把完整 CLI 注册成正式工具集合；它向 Agent 注入精选的运行时说明，列出常用
命令，并让 Agent 通过普通 shell CLI 和 `multica --help` 发现其余命令。它也支持独立绑定
Skills。本文不复用它的 task daemon；首期的 CLI 使用说明选择飞书式静态 Skill，不再增加
运行时 brief 装配器。

### 6.1 首期使用静态 Skill

首期直接参考飞书 CLI，先提供一个 `abd-im` Skill：

```text
skills/abd-im/
├── SKILL.md
└── references/
    ├── identity.md
    ├── messages.md
    └── groups.md
```

`SKILL.md` 只写：

- 什么情况下使用 `abdim`；
- 如何选择 `--as user|bot`；
- 默认使用 JSON 输出；
- 先通过 list/search 获取 ID，不猜 ID；
- 写操作失败后不盲目重试；
- 复杂流程应读取哪个 reference。

首期需要的命令、参数和示例直接写入 `SKILL.md` 或对应 reference。Agent 不依赖
`abdim --help` 才能完成首期流程。

Skill 不需要生成工具，也不需要声明权限列表。

Skill 只解释 direct/hosted 和 `--as user|bot` 的通用含义；当前 turn 究竟是哪种模式、代表哪个
owner，必须由 daemon 的运行时 prompt 明确给出，不能依赖静态 Skill 推断。

Codex 启动前，daemon 把 Skill 放到 Run 工作目录的：

```text
.agents/skills/abd-im/
```

Codex 使用原生 Skill 发现即可。首期不实现在线 Skill 市场、插件系统、第三方安装器或多
Provider Skill 抽象。

### 6.2 后续探索：用分层 `--help` 替代静态 Skill

静态 Skill 随 `abdim` 版本发布，无法自动反映用户刚开启的服务端权限。首期接受这个限制，
不同时建设第二套发现机制。

后续可单独验证是否用分层 `--help` 替代静态 Skill：

```bash
abdim --help
abdim message --help
abdim message history --help
```

候选的渐进式理解路径为：

```text
abdim --help
  -> abdim <domain> --help
  -> abdim <domain> <command> --help
```

这套 help 需要同时表达当前二进制支持的命令、user/bot SDK 就绪状态和对应身份当前可用的能力，
才能让变化及时对 Agent 可见；OpenIM 服务端仍按实际 SDK token 做最终鉴权，help 只负责发现。
具体协议、缓存和离线行为等到该阶段再设计。

首期不实现分层 help、affordance 解析层或 `abdim commands` JSON catalog。

## 7. 代码收敛

保留：

- conversation queue 和有界并发；
- Codex app-server adapter；
- conversation 级 Provider Session；
- daemon process lifecycle 和两个 SDK context 的登录生命周期；
- CLI JSON 输出；
- 服务端 business connection、hosting 配置和 business notification。

删除：

- `internal/agent/grant`；
- `internal/agent/proxy`；
- Run-private `internal/agent/access` socket；
- `commands.Run(methods)`；
- `ABDIM_AGENT_GRANT`、`ABDIM_AGENT_METHODS`、`ABDIM_AGENT_SOCKET`；
- `InboundToolsEnabled` 和 `abdim inbound tools ...`；
- `ReplySlot` 及其本地持久化；
- `internal/capability/business` 和 daemon 的 `SendBusinessText` 代发路径；
- 服务端为托管通知拉取完整历史和 `SendBusinessMessage` 代发路径；
- 只服务于上述链路的 capability wrapper。

如果 capability 包中有仍被普通 CLI 使用的请求结构或输入校验，只移动实际需要的部分，不为
旧架构保留兼容层。

## 8. 实施顺序

### 阶段 1：本地双 SDK

- Profile 保存 user 和 bot 两组 user ID 与凭据引用；
- 为两个身份使用隔离的 SDK 数据目录；
- daemon 在同一进程锁下管理两个 SDK context；
- 业务 CLI 支持 `--as user|bot` 并路由到对应 SDK。

验证：两个身份可同时登录且不争用 SDK 数据；同一读取命令按 `--as` 返回对应账号的数据；token
不出现在 Agent 环境或 CLI 输出中。

### 阶段 2：服务端托管触发，本地 user SDK 执行

- 保留服务端 business connection、hosting 配置和 business notification；
- notification 补充权威的 `owner_user_id`、conversation、trigger 和 instruction；
- daemon 校验 owner 与本地 user SDK 登录身份一致；
- user SDK 普通消息 callback 不触发 Agent，hosted 只由 business notification 触发；
- 历史和 trigger 内容通过本地 user SDK 获取；
- Agent 只返回 final text，daemon 使用入站引用通过本地 user SDK 自动发回原会话；
- 删除服务端拉历史和 `SendBusinessMessage` 代发路径。

验证：只有服务端标记为 hosted 的 conversation 才触发 Agent；除托管通知外，历史读取和最终回复
不调用 business API；最终消息由 owner 发出。

### 阶段 3：静态 Skill 和运行时身份提示

- 盘点当前 SDK 和现有 typed services，确定首期 CLI 命令清单；
- 新增 `skills/abd-im` 及 references；
- 为两种模式注入明确且不同的运行时身份说明；
- hosted 默认使用 `--as user`，direct 默认使用 `--as bot`；
- 入站消息按 conversation 启动 Agent turn；
- daemon 自动投递 final text：direct 使用 bot SDK，hosted 使用 user SDK；
- 删除 Reply Slot。

验证：真实 Agent 能命中 Skill；prompt 测试确认 direct/hosted 模式和相关身份不会混淆；端到端
测试确认两种模式的可见发送者正确；消息不串 conversation、不串回复。

### 阶段 4：删除旧工具装配

- 删除 grant、proxy、private socket、method snapshot 和 tools 开关；
- 删除 prompt 中的工具目录和 schema；
- 更新 README、ARCHITECTURE、spec 和测试。

验证：Agent 和人执行同一业务 CLI；代码中不存在 Run 私有工具目录。

## 9. 完成标准

- daemon 只负责 IM 在线、接收消息、启动 Agent turn 和发送结果，不负责组装工具权限；
- 服务端只负责 owner/agent 绑定、托管配置和触发通知；
- daemon 同时维护隔离的 user/bot SDK context，CLI 的 `--as` 只选择本地 SDK；
- Agent 在 direct/hosted turn 中收到明确的身份与回复模式；
- Agent 只返回 final text；daemon 自动使用 bot SDK 或 user SDK 发回原会话；
- 回复目标来自当前入站消息或 business notification，没有 task 和 Reply Slot；
- Agent 通过原生 `abd-im` Skill 学会使用普通 CLI；
- 没有 MCP、grant、Run-private proxy 或动态工具注入；
- conversation 隔离、Session resume 和最终回复链路通过端到端测试。
