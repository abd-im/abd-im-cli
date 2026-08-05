# AI 建议回复与聊天托管设计

## 1. 文档状态

| 项目 | 内容 |
| --- | --- |
| 状态 | Proposed，供产品与技术评审，尚未进入实现 |
| 范围 | ABD IM 的 AI 建议回复、AI 聊天托管、Agent 授权和多端管理 |
| 推荐身份模型 | Agent 经用户授权后代表该用户回复，消息保留服务端可信的 AI 来源 |
| 推荐首发范围 | 仅选中的私聊、单个建议草稿、纯文本、无业务工具 |
| 明确阻塞项 | 入站自动化 Agent 必须移除任意 Shell、文件和网络权限 |

本文档描述目标架构及取舍，不修改当前 `abdim` 的既有产品承诺。当前实现仍以
独立 bot 账号、本机 daemon 和 Codex app-server 为主，详见
[ARCHITECTURE.md](./ARCHITECTURE.md) 和 [spec.md](./spec.md)。

## 阅读导航

- 产品与身份取舍：第 5 节。
- 用户交互：第 6 节。
- 总体架构和信任边界：第 7 至 8 节。
- 权限、消息链路和状态机：第 9 至 11 节。
- 数据、API 和协议：第 12 至 14 节。
- 各仓库改造：第 15 节。
- 可靠性、安全、观测和测试：第 16 至 19 节。
- 实施、迁移、验收和待决项：第 20 至 24 节。

## 2. 背景

ABD IM 希望提供两类能力：

1. **建议回复**：收到消息后由 AI 生成草稿，用户确认或编辑后手动发送。
2. **AI 托管**：用户明确授权后，AI 在指定会话中代表用户自动回复。

交互可以参考 Telegram Chat Automation 的核心概念：连接一个 Agent、选择其可访问
的聊天范围、逐项授予权限、允许排除会话，并能随时暂停或移除 Agent。ABD 不应照搬
与当前业务无关的资料、礼物、动态等权限。

当前 `abd-im-cli` 已具备以下执行基础：

- OpenIM 入站消息归一化和事件去重。
- 按 `conversation_id` 建立 run 队列和事件绑定回复。
- run 级短期 grant、方法预算、历史消息窗口和附件额度。
- provider 私有 socket、typed tool proxy、取消和过期处理。
- 远端写操作幂等以及 `unknown` 结果保护。
- run、event、reply slot 和 operation 的有限持久化。

当前实现同时存在以下产品缺口：

- 登录的是独立 bot 账号，不是被授权的用户身份。
- 策略只有 profile 级 tools 总开关，没有 Agent 连接、会话范围和逐项权限。
- 所有非本人的私聊都会触发，群聊直接忽略。
- Agent 输出只能直接进入事件绑定回复，没有“草稿等待用户确认”的实体和状态。
- 配置和运行状态只在本机，Web 和其他设备不能统一查看、暂停或撤销。
- 消息没有服务端可信的 AI 来源，客户端可以伪造普通扩展字段。
- Codex 入站运行拥有 `danger-full-access` 且不需要审批，不适合处理远程不可信输入。

因此，目标方案应复用现有 run/grant/reply 原语，但不能把当前本机 bot 模式直接扩展为
生产级“代表本人托管”。

## 3. 目标与非目标

### 3.1 目标

- 用户可以连接一个受支持的 Agent，并明确知道它是否在线。
- 用户可以按会话范围和能力授权，默认采用最小权限。
- 每个会话可以独立设置为 `OFF`、`SUGGEST` 或 `AUTO`。
- 建议回复不会未经确认发送，过期语境下的建议不能继续使用。
- 托管回复只能发送到触发任务所属会话，不能由模型指定任意目标。
- 授权在运行中被撤销后，尚未发送的结果立即失效。
- AI 自动回复具有不可由普通客户端伪造的来源和完整审计链。
- 重复事件、进程重启和网络重试不会产生重复回复。
- Agent 故障不得阻塞正常 IM 收发，系统应安全降级到人工聊天。
- Web、桌面端和后续移动端可以看到一致的设置、建议和运行状态。

### 3.2 非目标

以下内容不进入第一版：

- 任意第三方 Agent/plugin 的动态安装市场。
- 自动删除消息、修改用户资料、管理成员或执行支付等高风险操作。
- 群聊全消息托管；群聊只在后续评估被 `@` 或被回复时触发。
- 长期 Agent 记忆、跨会话记忆或不受授权窗口限制的检索。
- 对不可信 Agent 提供用户机器上的任意 Shell、文件或网络访问。
- 用模型自报的“置信度”替代确定性的权限和安全策略。
- 保证外部模型提供商不保留数据；这需要单独的供应商与合规策略。

## 4. 术语

| 术语 | 含义 |
| --- | --- |
| Owner | 授权 Agent 代表自己处理消息的 ABD 用户 |
| Agent | Codex、Hermes、OpenClaw 或后续受支持的 AI provider |
| Executor | 实际运行 Agent 的受控进程，可以是本机 `abdim` 或云端 worker |
| Connection | Owner 与一个 Executor/Agent 的授权绑定 |
| Scope | Agent 可访问的会话集合及包含/排除规则 |
| Permission | 读取消息、生成建议、自动回复等细粒度能力 |
| Mode | 单个会话的 `OFF`、`SUGGEST` 或 `AUTO` 状态 |
| Task | 由一条有效入站消息触发的一次 AI 处理单元 |
| Suggestion | 等待用户使用、忽略或过期的 AI 草稿 |
| Delegated Send | 服务端验证授权后，以 Owner 身份发送 AI 回复的受限内部操作 |
| Policy Revision | 每次授权或范围变更后递增的版本，用于拒绝旧任务结果 |
| Anchor | Task 生成上下文时的最后消息 ID 和会话序号 |

## 5. 核心产品决策

### 5.1 身份模型

存在三种可行方案：

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| 独立 bot 账号 | 最接近当前实现；无需服务端代发 | 对方看到的是 bot；不能真正代表用户；设置依赖本机；不符合目标界面语义 | 仅保留为受控 bot 场景和开发验证 |
| 本机登录 Owner 账号 | 改造比服务端代发小；可沿用 SDK 发送 | CLI 持有用户 IM token；多端冲突；撤权和审计不可靠；本机离线即失效 | 不推荐作为产品架构 |
| 服务端 Delegated Send | 权限、撤权、来源、审计和多端一致性可由服务端保证 | 需要新增控制服务和受限代发路径；改造面较大 | **推荐** |

推荐语义如下：

- 自动回复的 `send_id` 是 Owner，而不是 Executor 或独立 bot。
- 服务端附加公开的 `AutomationInfo`，表明消息由哪个 Agent 自动生成；内部 run 和
  Connection 标识只保留在服务端审计中，不向聊天对端泄漏。
- 普通客户端不能自行写入可信的 `AutomationInfo`。
- 建议草稿经用户编辑和正常 SDK 发送后视为人工发送；审计可以记录建议被使用，
  但消息默认不标记为自动回复。
- 是否向聊天双方都展示 AI 标签属于产品透明度决策；协议必须保留来源，使客户端
  可以一致展示。推荐双方可见。

### 5.2 控制面位置

自动化连接、范围、建议和审计属于业务域，不应全部写入 OpenIM 核心消息服务。

推荐职责分配：

- 现有 `/chat` 业务服务负责用户会话鉴权，并承载或调用 Automation Service。
- Automation Service 负责连接、策略、任务、Executor 协议、建议和审计。
- OpenIM 核心只新增可靠事件出口、受限 Delegated Send 和 AI 来源字段。

当前工作区没有 `RUNTIME_CHAT_URL=/chat` 对应的服务端仓库。如果它在其他仓库，优先
在该业务后端增加 Automation 模块；如果不存在适合的业务后端，应创建独立服务，
而不是把产品控制面塞进 `abd-im-server/internal/api`。

### 5.3 执行位置

| 方案 | 优点 | 缺点 | 适用范围 |
| --- | --- | --- | --- |
| 本机 `abdim` Executor | 可复用本地 Codex 登录；数据可以尽量留在用户设备 | 依赖设备在线；连接恢复和升级复杂；本机安全边界必须收紧 | 首选接入方式之一 |
| 云端受管 Worker | 在线稳定、易扩缩容和观测 | 模型凭据与消息内容进入云端；成本和合规要求更高 | 后续托管版本 |
| 浏览器直接调用模型 | 前端实现快 | 暴露凭据；页面关闭即停止；无法安全自动发送 | 排除 |

Automation Service 对两类 Executor 使用同一任务协议。第一版可以只实现本机
`abdim`，但服务端数据模型不能假定 Executor 永久在线。

### 5.4 建议与托管的边界

建议模式和托管模式共用事件、授权、上下文和 Agent 执行链路，只在草稿生成后分叉：

- `SUGGEST`：保存 Suggestion 并推送给 Owner，绝不调用 Delegated Send。
- `AUTO`：执行确定性策略检查和会话 Anchor 校验，通过后才能调用 Delegated Send。

第一版不允许 `AUTO` 任务调用任何有副作用的业务工具。这样可以先证明文本回复的
授权、质量、撤销和幂等，再考虑更高风险能力。

### 5.5 流式输出

当前 `abdim` 可以把 provider 输出流式追加到一条 IM 消息。该能力不应直接用于建议
或托管回复，因为首个 packet 发出时完整内容尚未通过结构、内容和授权终态校验。

推荐行为：

- Executor 内部可以流式接收模型输出，用于取消和进度统计。
- Web 只展示“正在生成”等状态，不展示未经验证的远端正文片段。
- 服务端只在完整 Draft 生成并验证后创建 Suggestion 或 Delivery。
- AUTO 回复一次性进入 Delegated Send，不向聊天逐 token 输出。

## 6. 用户体验

### 6.1 Agent 连接

聊天页右上角机器人图标打开“聊天自动化”抽屉：

1. 展示已连接 Agent、provider、设备名称、最近心跳和在线状态。
2. 未连接时生成一次性设备码，引导用户在 `abdim connect` 中完成配对。
3. 配对完成后设置默认模式、会话范围和权限。
4. 用户可以暂停 Connection 或彻底移除 Agent。
5. 移除后立即关闭 Executor 会话、撤销设备凭据并取消未完成任务。

第一版每个 Owner 只允许一个具有回复能力的活动 Connection，避免多个 Agent 同时
生成或发送回复。数据模型可以保留多个历史 Connection，但不能同时进入 `ACTIVE`。

### 6.2 会话范围

参考 Telegram 提供两种范围模式：

- `ONLY_SELECTED`：只允许选中的会话。
- `ALL_PRIVATE_EXCEPT`：允许所有私聊，但排除指定会话。

安全默认值和第一版唯一开放值是 `ONLY_SELECTED`。`ALL_PRIVATE_EXCEPT` 只有在撤权、
新会话策略和性能经过验证后才开放。

选择会话时，服务端必须验证 Owner 当前确实拥有该会话的访问权。客户端提交的
`conversation_id` 不能直接成为授权事实。

### 6.3 权限界面

第一版只展示实现并能强制执行的权限：

- 读取新消息。
- 读取当前会话有限历史。
- 生成建议回复。
- 自动回复。
- 标记已读，可选且默认关闭。

附件读取、附件回复和业务工具应显示为未开放，或者完全不展示。不要展示资料、礼物、
动态等 ABD 尚未支持的 Telegram 权限。

### 6.4 会话内状态

每个会话可以设置：

| 模式 | 行为 |
| --- | --- |
| `OFF` | 不向 Agent 提供新消息，不创建 Task |
| `SUGGEST` | 生成草稿，用户确认后正常发送 |
| `AUTO` | 生成草稿并在二次鉴权通过后自动发送 |

输入框上方使用一条紧凑状态栏，而不是永久占用聊天区域：

- `SUGGEST`：展示一个主要建议，以及采用、重新生成、忽略操作。
- `AUTO`：展示“Agent 正在处理”以及取消/暂停图标。
- 授权受限：说明 Agent 只能处理当前会话，提供查看权限入口。
- Executor 离线：显示已暂停，不影响用户人工输入和发送。

用户点击建议后，文本进入现有编辑器，可以继续修改。点击不等于发送。其他设备上的
同一 Suggestion 应同步变为已采用或已失效。

### 6.5 人工接管

以下行为视为人工接管，并取消当前会话尚未发送的 AUTO Task：

- Owner 在该会话发送一条人工消息。
- Owner 点击暂停、关闭托管或切换到建议模式。
- Owner 取消正在运行的 Task。
- Owner 移除 Agent 或收窄授权范围。

第一版不建议“AI 和人工同时回复”。人工操作优先，明确的用户行为必须覆盖旧任务。

### 6.6 群聊策略

群聊不进入首发，但架构需要预留明确边界，避免以后把私聊策略直接放大：

- 群聊必须被显式选中，不能通过 `ALL_PRIVATE_EXCEPT` 隐式获得权限。
- Owner 必须仍是群成员；退群、被踢或群解散后立即撤销相关 Scope。
- 默认只在消息明确 `@Owner` 或回复 Owner 的消息时触发。
- AI 自动消息和其他 bot 消息不能再次触发自动化，防止 Agent 互相循环回复。
- 是否需要群主/管理员允许成员使用托管，由群治理策略决定，不能仅由客户端判断。
- 群聊使用更严格的频率和并发限制，并继续保证目标只能是原群。

## 7. 总体架构

```text
┌──────────────────────┐
│ abd-im-web / clients │
│ settings, suggestion │
└──────────┬───────────┘
           │ user API + realtime control channel
           ▼
┌─────────────────────────────────────────────────────────┐
│ Automation Service                                      │
│ connection │ policy │ context │ queue │ suggestion      │
│ lease      │ audit  │ revoke  │ delivery coordinator   │
└───────┬──────────────────┬──────────────────────┬────────┘
        │                  │                      │
        │ task lease       │ internal send        │ read context
        ▼                  ▼                      ▼
┌───────────────┐  ┌──────────────────┐  ┌────────────────┐
│ Agent Executor│  │ OpenIM delegated │  │ OpenIM message │
│ local / cloud │  │ reply boundary   │  │ query boundary │
└───────┬───────┘  └─────────┬────────┘  └────────────────┘
        │ draft result       │
        └────────────────────┘
                             ▲
                             │ durable post-persist event
                    ┌────────┴─────────┐
                    │ OpenIM msg path  │
                    └──────────────────┘
```

### 7.1 Automation Service

职责：

- 验证用户身份并管理 Connection、Scope、Permission 和 Mode。
- 接收消息落库后的可靠事件，只保存创建 Task 所需的最小引用。
- 对事件进行资格判断、去重、同会话串行和新消息覆盖处理。
- 从受控 OpenIM 查询接口构建有限上下文，而不是把用户 token 交给 Executor。
- 向 Executor 发放带租约、有效期和 Policy Revision 的任务。
- 验证结构化 Agent 结果，保存 Suggestion 或发起 Delegated Send。
- 向所有 Owner 客户端推送状态变化。
- 记录不含凭据和默认不含正文的审计事实。

### 7.2 OpenIM 集成边界

OpenIM 侧只承担不可绕过的消息事实：

- 消息完成鉴权并持久化后，输出至少一次投递的 automation event。
- 提供按 Owner 和 Conversation 限制的历史查询边界。
- Delegated Send 校验服务身份、Owner、Connection、Permission、Policy Revision、
  Conversation、Trigger、Anchor 和幂等键。
- 由服务端写入 `AutomationInfo`，普通发送路径清除客户端提交的同名字段。
- Delegated Send 进入与普通消息相同的序号、持久化、推送和离线通知链路。

### 7.3 Agent Executor

职责：

- 用设备凭据建立到 Automation Service 的出站连接并报告容量和健康状态。
- 领取 Task 后创建隔离 run，使用服务端提供的有限上下文生成结构化草稿。
- 只返回 `reply`、`no_reply` 或 `escalate`，不直接指定收件人或调用用户发送接口。
- 在任务取消、租约过期或 Connection 撤销时停止 provider session。
- 不持有 Owner 的 OpenIM token，也不能调用 Owner 控制 API。

当前 `abdim` 的 run manager、短期 grant、provider adapter、取消和本地运行目录可以复用。
本机 SDK 登录、入站监听和事件绑定 sender 在推荐架构中由服务端事件与 Delegated Send
替代。

## 8. 信任边界与威胁模型

### 8.1 可信组件

- Automation Service 及其数据库。
- OpenIM 内部 Delegated Send 和消息持久化链路。
- 服务间身份、密钥管理和授权版本存储。

### 8.2 不可信或部分可信输入

- 所有聊天消息正文、链接、附件名和引用内容。
- Agent 模型输出和模型发起的工具请求。
- Web/SDK 客户端提交的 Owner ID、Conversation ID 和来源字段。
- Executor 所在主机以及断线后重放的旧结果。
- 重复、乱序或延迟到达的消息事件。

### 8.3 当前实现的阻塞风险

当前 Codex adapter 使用 `approvalPolicy=never` 和 `danger-full-access`。即使 IM typed
tools 受 grant 保护，远程发送者仍可能通过提示注入诱导 Agent 读取本机文件、执行
命令或访问网络。IM grant 不能阻止这类本机副作用。

因此，入站自动化必须使用与交互式 Agent 工作台不同的运行配置：

- 默认无 Shell。
- 默认无任意文件读取和写入。
- 默认无任意网络访问。
- 只暴露任务输入和由服务端定义的窄工具。
- 工具参数使用结构化 schema，并由服务端再次鉴权。
- AUTO Task 不允许需要人工审批的副作用工具。

在该边界实现并通过安全测试前，只能交付建议回复的受控试验，不能开放生产自动托管。

### 8.4 主要威胁与控制

| 威胁 | 控制 |
| --- | --- |
| 客户端伪造 AI 标签 | 普通发送路径清除来源；仅内部 Delegated Send 写入 |
| Executor 越权读取其他会话 | 服务端构建上下文；Task 不提供任意历史查询能力 |
| 旧 Task 在撤权后发送 | Policy Revision + 发送前实时二次鉴权 |
| 模型把回复导向其他会话 | Task 不接受模型目标；目标只来自服务端 Trigger |
| 提示注入执行本机命令 | 入站 sandbox 禁用 Shell、任意文件和网络 |
| 重放 Executor 结果 | Task result nonce、租约、终态检查和幂等键 |
| 重复消息产生多次回复 | 事件唯一键、Task 唯一约束和 Delivery 幂等 |
| 人工与 AI 同时发送 | Anchor CAS、人工事件取消和同会话 fencing |
| 敏感正文进入日志 | 结构化日志只记录 ID、状态和错误分类 |
| 被移除 Executor 继续连接 | 设备凭据撤销、连接关闭和短期 access token |

## 9. 权限模型

### 9.1 权限定义

建议使用稳定枚举，不把 UI 文案或 provider 工具名作为授权契约：

| Permission | 说明 | 首发默认 |
| --- | --- | --- |
| `message.read_new` | 读取触发消息 | 建议/托管必需 |
| `message.read_history` | 读取当前会话 Anchor 之前的有限历史 | 开启，数量受限 |
| `message.suggest` | 创建 Suggestion | 建议模式必需 |
| `message.reply` | 调用 Delegated Send 回复触发消息 | 关闭，托管时显式开启 |
| `message.mark_read` | 代表 Owner 标记已读 | 关闭 |
| `attachment.read` | 读取受支持附件 | 延后 |
| `attachment.reply` | 回复附件 | 延后 |
| `tool.read.*` | 读取业务数据 | 延后，逐工具定义 |
| `tool.write.*` | 执行业务副作用 | 不进入首发 |

权限应按 Connection 保存，Task 只持有不可扩大的快照。Task 快照不能覆盖服务端当前
策略，发送和工具调用时始终重新取当前 Policy Revision。

### 9.2 Scope 规则

Scope 由模式和规则组成：

```text
ONLY_SELECTED:
  effective = selected conversations

ALL_PRIVATE_EXCEPT:
  effective = current private conversations - excluded conversations
```

显式 deny 永远优先。第一版只允许 `target_type=conversation`；联系人标签、会话分类和
组织部门规则延后，避免授权集合随外部属性变化而难以审计。

### 9.3 最终决策顺序

每条消息按以下顺序判定，任一步失败即不创建 Task：

1. 消息已持久化且不是系统、撤回、同步副本或 AI 自动消息。
2. Sender 不是 Owner，Owner 仍有权访问该 Conversation。
3. 存在唯一的活动 Connection，Executor 没有被撤销。
4. Conversation 命中 Scope 且没有显式排除。
5. Conversation Mode 不是 `OFF`。
6. Session Type 被当前版本支持；首发仅私聊。
7. 对应 Mode 所需 Permission 齐全。
8. 租户和安全策略没有禁止该 Conversation 或消息类型。
9. 该事件没有已存在的有效 Task。

`SUGGEST` 需要 `message.read_new + message.suggest`；`AUTO` 需要
`message.read_new + message.reply`。`message.read_history` 只决定上下文是否包含历史，
不应隐式附带。

### 9.4 撤销语义

每次 Connection、Scope、Permission 或 Mode 变化都递增 `policy_revision`：

- 排队 Task 在执行前比较 revision，不一致则取消。
- 运行 Task 收到撤销事件后主动中止。
- Executor 迟到结果必须携带原 revision，服务端发现不一致后丢弃。
- Delegated Send 再次比较当前 revision，不能只信任 Task 创建时的结果。
- 移除 Connection 同时撤销设备凭据和全部未完成租约。

## 10. 消息与任务处理流程

### 10.1 消息事件

Automation Service 只消费消息成功持久化后的事件。事件至少包含：

```text
event_id
conversation_id
server_message_id
sequence
sender_user_id
receiver_user_id (private chat)
group_id (group chat)
session_type
content_type
occurred_at
automation_source (optional)
```

这是中性的消息事实，OpenIM 事件出口不查询 Automation Connection，也不预先指定
Owner。Automation Service 根据 private chat 的 sender/receiver、活动 Connection 和
Scope 派生入站 Task；当 sender 本身是某个 Owner 时，同一事实用于人工接管和取消，
不能再次为该 Owner 创建入站 Task。群聊映射在后续版本结合 `@`/reply 信息和成员资格
完成。

事件默认不携带完整正文。资格判断通过后，Context Builder 使用内部受限接口读取触发
消息及允许的历史窗口。这减少广播消息总线中的正文副本，也让读取动作继续受当前授权
约束。

事件传输采用至少一次语义，不能假设 exactly-once。`event_id` 或稳定的
`owner_user_id + server_message_id` 必须有唯一约束。

### 10.2 新消息覆盖与防抖

联系人可能连续发送多条消息。第一版采用简单、确定性的覆盖规则：

- 同一 Conversation 的 Task 串行。
- 尚未进入 `RUNNING` 的旧 Task 被新入站消息标记为 `SUPERSEDED`。
- 正在运行的旧 Task 收到新消息后取消；为最新消息创建新 Task。
- 不尝试在模型运行中动态追加上下文。
- 可以设置很短的服务端防抖窗口，把最后一条消息作为 Anchor；窗口值是部署配置，
  不进入协议契约。

这样会牺牲少量响应速度，但避免对一组连续消息逐条回复。后续若需要显式消息批次，
再增加 `task_trigger_messages`，首发不提前引入。

### 10.3 上下文构建

Context Builder 根据 Permission 构建不可变输入：

- Owner、Conversation、Trigger 和 Anchor 的服务端标识。
- 当前消息的受支持内容。
- 允许时，Anchor 之前固定数量或 token 上限的同会话历史。
- 必要的会话类型和参与方显示信息。
- 语言、回复风格等 Owner 显式配置。
- 明确的输出 schema 和禁止事项。

不包含：

- 其他 Conversation 的历史。
- Owner IM token、服务端 token、设备凭据或内部地址。
- 任意本机路径、环境变量或 Agent 的历史工作目录。
- 未授权附件正文和业务数据。

自动化 Task 首发采用无状态 run，每次以服务端上下文为准，不复用长期 Codex thread。
长期 thread 会保留撤权前的数据并削弱上下文窗口的可审计性，应与未来交互式 Agent
工作台的 thread 复用分开评估。

### 10.4 Executor 租约

1. Executor 用设备 refresh credential 换取短期 access token。
2. 建立出站 TLS WebSocket，发送 provider、版本、能力和最大并发数。
3. 服务端通知可领取 Task，Executor 通过带 nonce 的请求取得完整输入。
4. Task 进入 `LEASED`，记录 `executor_id`、`lease_nonce` 和 `lease_expires_at`。
5. Executor 周期续租；断线或超时后租约失效。
6. 服务端只在 Task 仍处于该租约、revision 未变化时接受结果。

当前本机 Codex 可以报告容量 1。服务端设计应支持不同 Conversation 的有限并发，但
同一 Conversation 必须串行。不要把全局并发能力写死为当前 run manager 的行为。

### 10.5 Agent 输出

Executor 返回结构化结果，而不是把最终 stdout 直接当消息发送：

```json
{
  "task_id": "task_...",
  "lease_nonce": "...",
  "policy_revision": 12,
  "decision": "reply",
  "reply_text": "..."
}
```

`decision` 只允许：

- `reply`：生成一个文本草稿。
- `no_reply`：消息无需回复。
- `escalate`：需要用户人工处理。

服务端验证 UTF-8、长度、空文本、控制字符、允许的内容类型和任务终态。模型置信度
可以作为观测信息，但不能绕过任何权限或确定性策略。

### 10.6 建议回复流程

```text
message event
  -> policy check
  -> context build
  -> Agent Task
  -> draft validation
  -> Suggestion AVAILABLE
  -> realtime push to Owner devices
  -> user applies / dismisses / message becomes stale
```

用户采用后只把文本填入编辑器。最终发送继续走 Web 当前的 `createTextMessage` 和
`sendMessage` 路径。Suggestion 的采用状态用于多端同步，不是发送授权。

当 Conversation 最新序号超过 Suggestion Anchor、Owner 已发送消息、Mode 改变或
Suggestion 到期时，状态变为 `STALE`，客户端不能再次一键采用，但可以允许用户手动
复制已有文字。是否允许复制属于 UI 决策，不改变状态语义。

### 10.7 自动托管流程

```text
draft ready
  -> current connection/revision/permission check
  -> expected conversation anchor check
  -> content safety check
  -> create delivery with idempotency key
  -> Delegated Send
  -> normal OpenIM persistence and push
  -> delivery SENT / FAILED / UNKNOWN
```

Delegated Send 请求不能携带可由 Executor 选择的 Owner 或 Conversation。Automation
Service 从 Task 记录恢复目标，只提交 Task ID、Trigger、Anchor、文本和幂等键。

为了避免人工消息与 AI 回复竞争，OpenIM 发送边界必须支持 `expected_max_seq` 或等价的
会话 fencing，并在同一消息排序边界内完成比较和入队。如果当前消息链路无法提供该
原子条件，AUTO 模式应继续关闭，只交付 SUGGEST；仅在 Automation Service 中先查询
再发送存在竞态，不应被描述为可靠人工接管。

### 10.8 `unknown` 结果

网络中断时，调用方未收到响应不代表消息未送达：

- Delivery 在调用前以 `UNKNOWN` 写入。
- 服务端按 idempotency key 返回已有消息或已有终态，不能重新创建。
- 可确认失败时更新为 `FAILED`。
- 可确认成功时写入 `server_message_id` 并更新为 `SENT`。
- 无法确认时保持 `UNKNOWN`，后台仅查询结果，不重新发送。
- 管理端展示异常并允许人工确认，不提供“盲目重试”按钮。

## 11. 状态机

### 11.1 Connection

```text
PENDING_PAIRING -> ACTIVE <-> PAUSED
                       \-> OFFLINE
PENDING_PAIRING / ACTIVE / PAUSED / OFFLINE -> REVOKED
```

`OFFLINE` 表示凭据仍有效但 Executor 心跳超时；恢复连接后可回到 `ACTIVE`。首发在
离线期间不创建新 Task，也不在重连后追补陈旧回复。后续如需离线积压，必须另行定义
消息年龄、队列上限和 Owner 可见状态。

### 11.2 Task

```text
PENDING -> LEASED -> RUNNING -> DRAFT_READY -> COMPLETED
   |          |         |           |
   +----------+---------+-----------+-> CANCELLED
   +---------------------------------> SUPERSEDED
   +---------------------------------> EXPIRED
   +---------------------------------> FAILED
```

Task 终态不可逆。失败重试必须创建新的 attempt 或重新租约，但仍属于同一 Task 和同一
事件唯一键，不能生成第二条 Delivery。

### 11.3 Suggestion

```text
AVAILABLE -> APPLIED
AVAILABLE -> DISMISSED
AVAILABLE -> STALE
AVAILABLE -> EXPIRED
```

### 11.4 Delivery

```text
PENDING -> SENT
PENDING -> FAILED
PENDING -> UNKNOWN -> SENT / FAILED
```

## 12. 数据模型

以下是逻辑模型，最终表名和数据库类型由 Automation Service 所在仓库决定。
如果 ABD 部署存在租户边界，所有主表、唯一键、查询和审计都必须包含 `tenant_id`；
不能只依赖全局唯一的 user ID 隐式隔离租户。

### 12.1 `automation_connections`

| 字段 | 说明 |
| --- | --- |
| `id` | Connection ID |
| `owner_user_id` | 从认证上下文取得，不信任请求体 |
| `executor_id` | 已配对 Executor |
| `provider` | 固定 provider ID，如 `codex` |
| `state` | Connection 状态 |
| `scope_mode` | `ONLY_SELECTED` 或 `ALL_PRIVATE_EXCEPT` |
| `permissions` | 稳定权限集合 |
| `policy_revision` | 单调递增版本 |
| `last_heartbeat_at` | 最近 Executor 心跳 |
| `created_at/updated_at/revoked_at` | 生命周期时间 |

约束：每个 Owner 最多一个具有回复能力且未撤销的活动 Connection。

### 12.2 `automation_scope_rules`

| 字段 | 说明 |
| --- | --- |
| `connection_id` | 所属 Connection |
| `target_type` | 首发固定为 `conversation` |
| `target_id` | Conversation ID |
| `effect` | `ALLOW` 或 `DENY` |
| `created_by` | Owner 或管理员主体 |
| `created_at` | 创建时间 |

唯一键：`connection_id + target_type + target_id`。

### 12.3 `automation_chat_settings`

| 字段 | 说明 |
| --- | --- |
| `connection_id` | 所属 Connection |
| `conversation_id` | 会话 |
| `mode` | `OFF/SUGGEST/AUTO` |
| `updated_at` | 最近修改时间 |

唯一键：`connection_id + conversation_id`。没有记录时使用 Connection 的安全默认模式，
首发默认 `OFF`。

### 12.4 `automation_tasks`

| 字段 | 说明 |
| --- | --- |
| `id` | Task ID |
| `connection_id/owner_user_id` | 授权主体 |
| `conversation_id` | 固定目标会话 |
| `trigger_message_id` | 触发消息 |
| `anchor_message_id/anchor_seq` | 上下文和发送 fencing |
| `generation` | 同一 Trigger 的显式重新生成序号，事件首次创建固定为 1 |
| `mode` | Task 创建时模式 |
| `policy_revision` | 授权快照版本 |
| `provider/model/prompt_version` | 可审计的执行版本，不保存完整 prompt |
| `status` | Task 状态 |
| `executor_id/lease_nonce/lease_expires_at` | 租约 |
| `attempt` | 有界执行次数 |
| `failure_code` | 稳定错误分类，不保存原始 provider 错误 |
| `created_at/started_at/finished_at` | 生命周期时间 |

唯一键为 `connection_id + trigger_message_id + generation`。消息事件只能创建
`generation=1`；只有已鉴权的 regenerate API 可以原子创建下一 generation，因此重复
事件不能伪装成重新生成。按 `connection_id + conversation_id + status` 建索引支持
取消和同会话调度。

### 12.5 `automation_suggestions`

| 字段 | 说明 |
| --- | --- |
| `id/task_id` | Suggestion 与来源 Task |
| `owner_user_id/conversation_id` | 访问边界 |
| `anchor_seq` | 陈旧判断 |
| `text_ciphertext` | 加密保存的草稿正文 |
| `status` | Suggestion 状态 |
| `expires_at` | 到期时间 |
| `created_at/updated_at` | 生命周期时间 |

一个首发 Task 最多产生一个 Suggestion，避免不必要的成本和复杂 UI。重新生成创建新
Task，并使旧 Suggestion 失效。

### 12.6 `automation_deliveries`

| 字段 | 说明 |
| --- | --- |
| `id/task_id` | Delivery 和来源 Task |
| `idempotency_key` | 全局唯一幂等键 |
| `trigger_message_id/expected_max_seq` | 安全发送条件 |
| `status` | `PENDING/SENT/FAILED/UNKNOWN` |
| `server_message_id` | 确认发送后的消息 ID |
| `failure_code` | 稳定错误分类 |
| `created_at/updated_at` | 生命周期时间 |

每个 Task 最多一个 Delivery。

### 12.7 `automation_audit_events`

记录：

- Connection 配对、暂停、恢复和移除。
- Permission、Scope、Mode 和 revision 变化。
- Task 创建、取消、超时、升级人工和终态。
- Delegated Send 的目标摘要、Agent、结果和消息 ID。

默认不记录：

- 用户 token、设备 refresh credential、lease nonce。
- 完整 prompt、历史正文、模型原始输出和本机路径。
- Suggestion 正文；正文只存在专用加密字段并按短期保留策略删除。

建议默认保留策略需由合规确认。技术初始值可以是 Suggestion 正文 24 小时、Task 运行
元数据 30 天、授权和发送审计 180 天，并允许按部署收紧，而不是写死在协议中。

### 12.8 事务 Outbox

Automation Service 的状态变化和实时事件使用事务 Outbox：

- Task/Suggestion/Connection 状态与对应 outbox record 在同一数据库事务中提交。
- publisher 至少一次发送，客户端按 event ID 去重。
- outbox 只保存状态事件需要的有限 payload，不复制 prompt 或历史正文。
- 发送成功后异步清理，保留时间覆盖允许的客户端 cursor 恢复窗口。

## 13. API 设计

### 13.1 用户控制 API

所有接口从用户认证上下文取得 Owner，不接受调用方指定 Owner：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/automation/connections/pairing` | 创建一次性 Executor 配对码 |
| `GET` | `/automation/connections/current` | 获取当前 Connection 和健康状态 |
| `PATCH` | `/automation/connections/{id}` | 暂停或恢复 Connection |
| `DELETE` | `/automation/connections/{id}` | 撤销并移除 Agent |
| `PUT` | `/automation/connections/{id}/policy` | 原子更新范围模式和权限 |
| `PUT` | `/automation/connections/{id}/scopes/{conversationID}` | 添加 allow/deny 规则 |
| `DELETE` | `/automation/connections/{id}/scopes/{conversationID}` | 删除规则 |
| `PUT` | `/automation/conversations/{conversationID}/mode` | 设置会话模式 |
| `GET` | `/automation/conversations/{conversationID}/status` | 获取模式、运行和 Agent 状态 |
| `GET` | `/automation/suggestions` | 按 Conversation 查询可用 Suggestion |
| `POST` | `/automation/suggestions/{id}/apply` | 标记已进入编辑器 |
| `POST` | `/automation/suggestions/{id}/dismiss` | 忽略建议 |
| `POST` | `/automation/suggestions/{id}/regenerate` | 使旧建议失效并创建新 Task |
| `POST` | `/automation/tasks/{id}/cancel` | 人工取消运行 |
| `GET` | `/automation/audit` | 分页查看有限审计信息 |

策略更新使用 `If-Match` 或请求中的 `expected_policy_revision`，防止多个设备互相覆盖。
成功后原子递增 revision 并返回完整新策略。

策略示例：

```json
{
  "expected_policy_revision": 11,
  "scope_mode": "ONLY_SELECTED",
  "permissions": [
    "message.read_new",
    "message.read_history",
    "message.suggest"
  ]
}
```

### 13.2 Executor 配对与连接 API

配对采用设备码流程：

1. Web 创建短期、一次性的 `user_code/device_code`。
2. 用户在本机执行 `abdim connect` 并输入 `user_code`。
3. CLI 轮询或等待授权，交换得到 Executor refresh credential。
4. 服务端只保存 credential hash 或公钥，CLI 使用系统密钥环或 `0600` 文件保存。
5. Executor 使用 refresh credential 换取短期连接 token。

Executor 协议至少支持：

- `hello/capabilities`
- `heartbeat`
- `task.available`
- `task.lease`
- `task.started`
- `task.result`
- `task.failed`
- `task.cancel`
- `connection.revoked`

每条消息包含协议版本、request ID 和 Connection ID。服务端不能通过该连接暴露用户
控制 API 或通用 OpenIM API。

### 13.3 内部 Delegated Send

内部 RPC 建议表达为：

```text
SendAutomationReply(
  task_id,
  policy_revision,
  trigger_message_id,
  expected_max_seq,
  idempotency_key,
  text
)
```

服务端从 Task 恢复 Owner 和 Conversation。实现必须拒绝：

- 请求体提供或覆盖 Owner/Conversation。
- Connection 已暂停、离线策略不允许、被撤销或 revision 不一致。
- Conversation 已被排除、Mode 不再是 AUTO 或 Permission 已关闭。
- Trigger 不属于该 Conversation。
- 最新序号不等于 `expected_max_seq`。
- 文本为空、超长、类型不支持或 idempotency digest 冲突。

### 13.4 客户端实时通道

Automation 状态不应作为普通聊天消息写入历史。Web 使用业务 WebSocket 或 SSE 接收：

- Connection 在线/离线和策略变化。
- Task 排队、运行、取消和失败。
- Suggestion 创建、采用、失效和过期。
- AUTO 发送确认或 `unknown` 告警。

断线重连使用递增 cursor 恢复，cursor 过期时客户端重新读取 Connection、当前会话状态
和 Suggestion 快照。首发不建议把这些事件塞进 OpenIM 自定义消息，避免污染会话排序和
未读数。

## 14. 消息协议

在 `protocol/sdkws.MsgData` 增加可选字段，示意如下：

```proto
enum AutomationMessageMode {
  AUTOMATION_MESSAGE_MODE_UNSPECIFIED = 0;
  AUTOMATION_MESSAGE_MODE_AUTO_REPLY = 1;
}

message AutomationInfo {
  string agentID = 1;
  string agentName = 2;
  string triggerMessageID = 3;
  AutomationMessageMode mode = 4;
}

message MsgData {
  // existing fields
  AutomationInfo automationInfo = <new field number>;
}
```

要求：

- 字段号在修改 `protocol` 时根据当前最新 schema 分配，不能复用历史字段号。
- 外部 SendMessage 入口无条件丢弃客户端提交的 `automationInfo`。
- 内部 Delegated Send 在服务端构造该字段。
- `agentID` 是可公开的稳定 Agent 类型/实例标识，不是 Executor、Connection 或内部
  provider credential；内部 `task_id/run_id/connection_id` 不发给聊天参与者。
- 字段随消息落库、同步、历史查询、推送和转发保持一致。
- SDK 本地库和 converter 需要保留该字段。
- 新客户端展示 AI 来源，旧客户端忽略未知字段并正常显示正文。
- 撤回、引用和转发的展示规则需要 UI 单独定义，但不能删除原始来源审计。

不推荐只使用 `ex` 或 `attachedInfo`：这些字段在普通客户端发送路径中可被提交，若没有
服务端签名和清洗，不能作为可信来源。增加显式字段和服务端写入边界更容易审计。

## 15. 各仓库改造

### 15.1 `abd-im-cli`

保留：

- `internal/agent/run` 的取消、deadline 和 Conversation 队列原语。
- `internal/agent/grant` 的短期凭据、过期、预算和撤销思路。
- Codex/ACP provider adapter 和受控输出流。
- `control.db` 作为 Executor 本地运行日志和崩溃恢复缓存。

改造：

- 新增 `abdim connect/disconnect/status` 的设备配对流程。
- 新增 Automation Service connector、心跳、Task lease 和 cursor 恢复。
- Task 输入改为服务端上下文信封，不再从本机 SDK 入站回调构造 prompt。
- provider 输出改为结构化 DraftResult，不直接调用本地 reply sender。
- 移除自动化 run 的 Owner IM token、通用本机环境和危险工具权限。
- 本地持久化不保存完整消息上下文和 Suggestion 正文。
- 当前 bot profile 模式暂时保留为 legacy/受控部署，不能与 delegated mode 混用。

### 15.2 `abd-im-server`

OpenIM 核心最小改造：

- 消息持久化成功后的可靠 automation event adapter/outbox。
- 内部 Delegated Send RPC 及服务身份鉴权。
- Anchor/expected sequence 的原子检查或同一排序边界 fencing。
- 服务端生成并清洗 `AutomationInfo`。
- 消息存储、同步、历史、推送和转发链路透传来源。

如果 Automation Service 以 OpenIM 新服务部署，可在该仓库新增独立 `rpc/automation`
和存储模块；如果业务 `/chat` 服务更适合承载控制面，则本仓库只实现上述最小边界。

### 15.3 `protocol`

- 增加 `AutomationInfo`。
- 增加内部 Delegated Send 请求/响应，或为独立 automation proto 分配服务契约。
- 增加稳定错误码：policy stale、scope denied、anchor stale、connection revoked、
  delivery unknown 等。
- 生成 Go/JS 等现有目标代码并验证旧客户端兼容。

### 15.4 `abd-im-sdk-core`

- 消息模型、converter 和本地数据库保存 `AutomationInfo`。
- 历史查询和消息 listener 返回该字段。
- 普通 `SendMessage` 不暴露设置可信来源的便捷 API；即使类型可构造，服务端也清除。
- 验证 Stream、引用、转发、撤回和离线同步不丢失来源。

### 15.5 `abd-im-sdk-js-wasm`

- 暴露只读 `automationInfo` 类型。
- 生成和发布与 core/protocol 对齐的 WASM 版本。
- 不把 Automation 控制 API 放进 IM SDK；Web 通过业务 API 管理连接和建议。

### 15.6 `abd-im-web`

- 新增 Automation API client 和 Zustand store。
- ChatHeader 机器人图标打开自动化抽屉。
- 实现 Agent 配对状态、Scope 选择、Permission 和移除操作。
- 在会话内提供 `OFF/SUGGEST/AUTO` 控制。
- ChatFooter 上方展示 Suggestion 或运行状态，采用后写入现有 CKEditor。
- MessageItem 根据可信 `automationInfo` 展示“AI 回复 · Agent”。
- 订阅业务实时通道并实现 cursor 恢复。
- 多端策略更新使用 revision，冲突时刷新而不是覆盖。

## 16. 可靠性与一致性

### 16.1 语义

- 消息事件：至少一次。
- Task：每个事件自动创建至多一个 `generation=1` Task；用户显式重新生成可以创建下一
  generation，每个 Task 可有有界执行 attempt。
- Suggestion：每个首发 Task 至多一个活动 Suggestion。
- Delivery：每个 Task 至多一次可观察发送，网络层允许重试同一幂等请求。
- 不宣称跨服务全局 exactly-once；通过唯一键、幂等和查询恢复实现可审计去重。

### 16.2 同会话顺序

调度键为 `owner_user_id + conversation_id`：

- 同键一次只有一个有效 Task 进入 Agent 执行或发送阶段。
- 新入站消息使旧 Task `SUPERSEDED`。
- 人工发送事件提高会话 generation，并取消旧 Task。
- Executor 的不同 Conversation 可以在其容量范围内并行。

分布式部署使用数据库条件更新、队列分区或租约 fencing，不能依赖单进程内存锁。

### 16.3 崩溃恢复

- Automation Service 重启后从数据库恢复 PENDING/LEASED/RUNNING Task。
- 过期租约回到可调度状态或按尝试次数失败。
- 已产生 Delivery 的 Task 不重新生成第二个 Delivery。
- Executor 重连后提交旧 nonce 结果会被拒绝。
- `UNKNOWN` Delivery 仅通过 idempotency query 对账。
- Web 重连读取快照并用 cursor 补充状态事件。

### 16.4 降级

| 故障 | 行为 |
| --- | --- |
| Automation Service 不可用 | IM 正常工作；不创建建议和托管回复 |
| Executor 离线 | Connection 显示离线；不无限积压 AUTO Task |
| Agent 超时 | Task 失败或升级人工；不发送占位消息 |
| 实时通道断开 | Web 使用快照和 cursor 恢复 |
| Delegated Send 超时 | Delivery 标记 UNKNOWN 并查询，不盲重试 |
| Policy 存储不可用 | fail closed，不创建或发送 |
| 历史读取失败 | 可按策略只使用 Trigger，不能扩大到其他来源 |

### 16.5 限流与资源边界

- 按 tenant、Owner、Connection、Conversation 和 Sender 设置有界 Task 速率。
- 每个 Executor 声明最大并发，服务端不得超发租约。
- 每个 Conversation 只有有限待处理项，新消息优先 supersede 旧项而不是无限排队。
- Context、Draft、附件、provider token 和运行时间都有硬上限。
- AI 自动消息永不触发新 Task；群聊还需要跨 Agent 的循环和风暴检测。
- 超出预算时 fail closed 或降级人工，不发送由系统拼接的占位回复。

## 17. 安全与隐私要求

### 17.1 凭据

- Owner 的 IM token 不发给 Executor 或 Agent。
- Executor refresh credential 与 Connection/设备绑定，可独立撤销。
- 服务间 Delegated Send 使用服务身份和短期凭据。
- 数据库只保存 credential hash 或公钥材料。
- CLI 优先使用系统密钥环；回退文件必须为 Owner-only 权限。
- 凭据、配对码、nonce、prompt 和正文不写日志。

### 17.2 数据最小化

- 消息事件总线只携带引用和资格判断所需元数据。
- Context Builder 只读取同会话、Anchor 之前、固定窗口内的数据。
- Suggestion 正文单独加密并短期保留。
- Task 和审计表默认只保存 ID、状态、时间和错误分类。
- Provider 数据保留策略必须展示给 Owner，并支持部署级关闭云端 provider。

### 17.3 工具安全

首发 Agent 没有工具，只返回文本 Draft。后续开放工具时：

- 每个工具有独立稳定 Permission，而不是一个 `tools_enabled` 总开关。
- 工具只能由 Automation Service 提供结构化代理，不能由 Executor 直连数据库或 IM。
- read tool 仍受 Conversation/Owner/Anchor 限制。
- write tool 生成 Action Proposal，必须人工审批；AUTO 不自动批准。
- 工具调用和结果使用大小、次数、deadline 和敏感字段过滤。

### 17.4 内容安全

确定性检查至少包括：

- UTF-8、长度和空文本校验。
- 移除不可见控制字符和不支持的富文本结构。
- 阻止模型输出内部凭据、路径或运行时错误详情。
- 可按部署启用内容审核和敏感行业策略。
- `escalate/no_reply` 始终优先于强制生成一条看似合理的回复。

## 18. 可观测性

### 18.1 指标

- automation event 接收、去重和延迟。
- Policy 拒绝原因分布。
- Task 排队时间、执行时间、取消、超时和 superseded 比例。
- Executor 在线数、版本、心跳延迟和租约失败。
- Suggestion 生成、采用、忽略和过期比例。
- AUTO 发送成功、失败、unknown、anchor stale 和人工接管比例。
- 每 provider token/成本，但不把正文作为 label 或日志。

所有指标 label 必须有界，不能使用 user ID、conversation ID 或 message ID 作为高基数
label；这些标识只出现在受控追踪和审计查询中。

### 18.2 日志与追踪

跨服务使用 `event_id/task_id/run_id/delivery_id` 关联。日志只记录：

- 状态迁移。
- 稳定错误码。
- provider 和版本。
- 有界耗时、attempt 和 policy revision。

默认不记录 message text、Suggestion text、prompt、Agent 原始输出和 tool payload。

### 18.3 告警

- Delivery UNKNOWN 持续积压。
- 重复 idempotency digest 冲突。
- Executor 大面积离线或版本不兼容。
- Task 延迟或失败率异常。
- policy stale、anchor stale 或伪造来源请求异常增长。
- Automation Service 故障影响正常 IM 时立即最高级告警；按设计两者应隔离。

## 19. 测试策略

### 19.1 单元测试

- Permission 与 Scope 的完整决策矩阵。
- deny 优先、Mode 覆盖和 policy revision 递增。
- Task、Suggestion、Connection 和 Delivery 状态机非法迁移。
- Agent 输出 schema、长度和内容校验。
- 幂等键、digest 冲突和 UNKNOWN 对账。
- 日志、错误和审计不泄漏正文与凭据。

### 19.2 组件测试

- 消息 event 重复、乱序、延迟和 cursor 恢复。
- Executor 配对、token 刷新、心跳、租约超时和撤销。
- 新消息 supersede、人工发送取消和多设备策略冲突。
- Context Builder 不跨 Conversation、不越过 Anchor、不读取未授权附件。
- 普通 SendMessage 伪造 `AutomationInfo` 被清除。
- Delegated Send 的 scope、revision、permission、trigger 和 anchor 拒绝。

### 19.3 安全测试

- 远程消息诱导读取本机文件、环境变量、Shell 和网络均失败。
- Agent 输出伪造 Owner、Conversation、system 字段或 AI 来源均无效。
- 被撤销 Executor 重放 Task result、nonce 或 delivery 均失败。
- Web 修改请求体 Owner ID 不能访问其他用户配置。
- Conversation 被删除、拉黑、退出或权限变化后不能继续读取或回复。
- prompt injection 不能扩大 typed tool 或历史消息范围。

### 19.4 端到端场景

至少使用 Owner、联系人和另一个无关用户：

1. Owner 只授权联系人会话，联系人收到建议，无关用户消息不触发。
2. 建议在 Owner 人工回复后立即失效。
3. 同一入站事件重复投递只产生一个 Suggestion 或 Delivery。
4. AUTO 运行期间撤销 `message.reply`，结果被丢弃。
5. AUTO 运行期间新消息到达，旧 Task 被 supersede。
6. AUTO 运行期间 Owner 人工发送，Delegated Send 的 Anchor CAS 失败。
7. Executor 离线和 Automation Service 重启不影响正常聊天。
8. AI 自动消息在新旧 SDK/Web 客户端都可正常读取，旧客户端只缺少来源标签。
9. 服务端超时后查询 idempotency key，不重复发送。
10. 移除 Agent 后旧设备不能重新连接或读取任务。

## 20. 分阶段实施

### Phase 0：架构与安全基础

交付：

- 最终确认“独立 bot”还是“代表本人”；本文推荐后者。
- 确认 Automation Service 所在业务仓库。
- 定义 Permission、Policy Revision、Task 和 `AutomationInfo` 契约。
- 完成入站 Agent sandbox，证明 Shell、文件和任意网络不可达。
- 验证 OpenIM 能提供可靠 post-persist event 和 Anchor 原子发送条件。

退出条件：威胁模型、安全边界和服务职责通过评审；否则不进入 AUTO 实现。

### Phase 1：建议回复 MVP

范围：

- 一个 Owner 连接一个本机 `abdim` Executor。
- `ONLY_SELECTED` 私聊。
- `OFF/SUGGEST`，一个纯文本 Suggestion。
- 无工具、无附件、无长期 thread。
- 多端策略和 Suggestion 状态同步。

退出条件：越权、跨会话、陈旧建议、撤权和提示注入测试全部通过，且 Agent 故障不影响
正常 IM。

### Phase 2：自动托管 MVP

范围：

- 增加 `message.reply` 和 `AUTO`。
- 内部 Delegated Send、`AutomationInfo`、Anchor CAS 和 Delivery 对账。
- 人工接管、暂停、移除、审计和 unknown 运维入口。
- 仍只支持选中私聊和纯文本，无副作用工具。

退出条件：运行中撤权绝不发送、人工回复竞态可原子拒绝、重复事件不重复发送、来源
不可伪造。

### Phase 3：范围扩展

候选能力逐项评审，不整体打包：

- `ALL_PRIVATE_EXCEPT`。
- 群聊仅在被 `@` 或被明确回复时触发。
- 受支持附件。
- 云端受管 Executor。
- 只读业务工具和人工批准的写工具。
- 静默时段、人工升级队列等业务策略。

每项都需要独立 Permission、测试和审计，不恢复当前 profile 级 `tools_enabled` 总开关。

## 21. 迁移与兼容

当前 `abdim setup` 保存 bot 账号 token 并监听该 bot 的私聊。推荐迁移策略：

- 保留现有模式为 `legacy_bot`，用于受控部署和回归测试。
- 新 delegated 模式使用独立配置目录或显式 profile type，避免共享 token、数据库和 socket。
- 不自动把现有 `InboundToolsEnabled=true` 转换成任何新 Permission。
- 用户必须在 Web 中重新配对并选择会话范围。
- delegated 模式稳定后，再决定是否弃用 legacy bot；不在首次发布强制迁移。
- `AutomationInfo` 为可选协议字段，支持新服务与旧客户端滚动升级。

建议服务端功能开关：

- `automation.suggest.enabled`
- `automation.auto_reply.enabled`
- `automation.delegated_send.enabled`
- `automation.provider.<id>.enabled`
- `automation.scope.all_private_except.enabled`

AUTO 开关必须可以独立紧急关闭，而不影响 Suggestion 和普通 IM。

## 22. 主要取舍汇总

| 决策 | 推荐 | 得到 | 放弃/成本 |
| --- | --- | --- | --- |
| 回复身份 | 服务端代表 Owner | 符合产品语义、可信撤权和审计 | 新增代发边界和协议 |
| 控制面 | 业务 Automation Service | 与 OpenIM 核心解耦，多端一致 | 新服务运维成本 |
| 首发模式 | 先 Suggest 再 AUTO | 风险和质量可分阶段验证 | 自动化价值稍晚交付 |
| 首发范围 | ONLY_SELECTED 私聊 | 最小权限、易解释 | 用户需要逐个选择 |
| 上下文 | 服务端有限窗口 | 可审计、撤权明确 | 不具备长期记忆 |
| Agent session | Task 无状态 | 不残留撤权前数据 | 每次上下文成本更高 |
| 自动工具 | 首发禁用 | 显著降低提示注入后果 | 不能自动执行业务动作 |
| Executor | 支持本机/云端统一协议 | 保留部署选择 | connector 协议需维护 |
| 实时状态 | 业务 WS/SSE | 不污染聊天历史和未读 | 客户端多一条连接 |
| AI 来源 | 显式协议字段 | 可验证、可兼容 | protocol/SDK 全链路改造 |
| 人工竞态 | Anchor 原子 CAS | 不会在人工接管后误发 | 需要消息排序边界支持 |

## 23. 验收标准

首发建议回复必须满足：

- 未授权、被排除或 `OFF` 会话不会把正文交给 Agent。
- 同一事件只产生一个逻辑 Task，不同 Conversation 不共享上下文。
- 新消息、人工回复、策略变化和到期会使旧 Suggestion 失效。
- Executor 不能访问 Owner token、其他会话、Shell、任意文件或任意网络。
- Agent 离线、服务重启和实时通道断开不影响普通聊天。
- Web 多端能一致查看 Connection、Mode、Task 和 Suggestion 状态。

自动托管额外必须满足：

- 每条自动回复来自有效 Trigger，目标不能由模型覆盖。
- 运行中暂停、撤权或收窄 Scope 后，旧结果绝不发送。
- 人工回复或新消息与 AUTO 发送竞争时，通过原子 Anchor 条件拒绝旧结果。
- 重复事件、结果重放和网络超时不会产生第二条消息。
- 普通客户端不能伪造 `AutomationInfo`。
- Delivery `UNKNOWN` 可查询和审计，系统不会盲目重试。
- AI 回复来源在同步、历史、推送、引用和旧客户端兼容链路中行为明确。

## 24. 待决问题

实现前需要产品或架构评审明确：

1. `/chat` 业务服务的实际仓库和维护边界是什么，Automation Service 放在哪里？
2. AI 来源标签对 Owner 可见、对双方可见，还是按租户配置？推荐双方可见。
3. Owner 是否允许同时连接多个只读 Agent？首发建议只允许一个活动 Agent。
4. 本机 Codex 能否提供真正禁止 Shell、文件和网络的入站运行配置；若不能，采用何种
   容器或受管 Worker？
5. OpenIM 当前消息排序链路能否实现 `expected_max_seq` 原子比较；若不能，AUTO 必须
   延后还是接受较弱语义？本文不建议接受较弱语义。
6. Suggestion 正文、Task 元数据和审计的正式保留期限及数据驻留要求是什么？
7. 模型供应商、模型版本、成本上限和内容审核由谁配置？
8. 被拉黑、阅后即焚、私密会话和合规留痕会话是否一律禁止自动化？
9. 建议被用户修改后发送，是否需要向对方展示 assisted 来源？本文默认不展示。
10. 群聊将来由普通成员自行授权即可，还是必须群主/管理员额外允许？

## 25. 推荐结论

推荐把现有 `abdim` 定位为可复用的 Agent 执行器，而不是最终授权控制面：

1. 服务端新增 Automation Service，统一管理 Connection、Scope、Permission、Mode、
   Task、Suggestion 和审计。
2. OpenIM 只提供可靠消息事件、有限上下文读取、原子 Delegated Send 和可信来源。
3. CLI 不再持有 Owner IM token，只通过短期 Task lease 生成结构化草稿。
4. 先交付 `ONLY_SELECTED + SUGGEST + 纯文本 + 无工具`，验证安全与质量。
5. 只有在 sandbox、撤权、幂等、来源和 Anchor CAS 全部成立后才开放 AUTO。

这一方案比继续扩展独立 bot 复杂，但复杂度来自“代表用户发送”必须具备的真实安全和
一致性要求。若产品最终只需要独立 bot，应保留当前架构并缩小目标，而不应在 UI 上把
它描述成“代表本人托管”。
