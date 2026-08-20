# Agent 工作区最小接入方案

状态：基础链路已实现；在线控制与真实环境验收待完成

前端原型：兄弟仓库 `abd-im-web/docs/agent-workbench/index.html`

## 1. 结论

Agent 工作区继续复用 OpenIM 群聊，不新增 Agent 会话表、SessionType、GroupType、
WebSocket 或消息存储：

- 一个 Agent 工作区会话对应一个群聊 `conversation_id`。
- `GroupInfo.ex` 标识这个群是否使用 Agent 工作区界面。
- 一次用户提问对应一条 `agent_run_v1` Stream Message。
- reasoning summary、工具摘要和最终回答作为同一条流消息的 packet 追加。
- 普通群聊继续只接收现有文本 Stream Message，不展示运行过程。
- Codex、ACP 或其他外部 Agent 的 session ID 只保存在 `abd-im-cli` 本地。

因此，会话分类和 IM 传输层改动很小。当前实现已经接通会话分类、独立 Web 页面、结构化
Run stream、reasoning summary 和工具生命周期；取消/审批控制仍受真实双登录验证门槛阻塞。

| 范围 | 改动 |
| --- | --- |
| OpenIM protocol | 无 |
| OpenIM server 数据表 | 无 |
| Kafka、Redis、WebSocket | 无 |
| SDK 数据表 | 无，群资料已经保存 `Ex` |
| Web 会话分类 | 小：解析群 `Ex` 并拆分两个界面 |
| Web Agent Run 渲染 | 中：增加结构化 Stream renderer |
| `abd-im-cli` 回复路由 | 小：根据群 `Ex` 选择文本流或 Agent Run 流 |
| provider 事件适配 | 中：输出 summary reasoning 和工具摘要 |

## 2. 核心边界

### 2.1 会话身份

工作区仍使用普通读群会话：

```text
group_id        = <OpenIM group ID>
conversation_id = sg_<group_id>
```

`conversation_id` 继续作为 event、run、reply slot、队列和 provider session 的隔离键。
Agent 工作区与普通群聊的区别是产品展示模式，不是新的 IM 传输语义。

### 2.2 外部 Agent session

服务端和消息内容都不保存外部 Agent session ID。现有本地映射保持不变：

```text
(profile_id, conversation_id, provider) -> provider session_ref
```

删除或迁移 `abd-im-cli` 本地数据后，可以根据同一 IM 会话历史创建新的 provider
session，但不能恢复丢失的外部 session。这个行为是本地映射边界的一部分。

### 2.3 展示内容

工作区只展示用户可理解、可验证的运行摘要：

- reasoning summary；
- 工具名称、目标摘要、结果状态和耗时；
- 必要的工具结果摘要；
- 生成的文件或其他 artifact 引用；
- 最终回答和 run 状态。

不保存或展示原始 Chain-of-Thought、完整终端日志、网页全文、逐 token provider 事件或
大型工具输出。大型结果保存为已有附件或后续 artifact，并在 packet 中只放引用和摘要。

## 3. 群扩展标识

### 3.1 格式

创建工作区群时写入：

```json
{
  "abd": {
    "kind": "agent_workspace",
    "version": 1
  }
}
```

规则：

- `abd` 是保留命名空间，不能占用其他业务的顶层字段。
- `kind == "agent_workspace"` 表示使用 Agent 工作区界面。
- `version` 只描述工作区元数据格式，不描述消息 packet 格式。
- 不在 `Ex` 中保存 provider、provider session ID、token 或本地路径。
- 更新群资料时解析并合并 JSON，不能覆盖其他顶层扩展。
- 缺失、JSON 非法或 `kind` 未识别时按普通群聊处理。

`Ex` 只做产品分类，不能作为权限依据。消息发送、群访问和 Agent 工具权限仍由现有群
成员关系、reply slot 和 run grant 决定。因此不需要为了保护该标记新增服务端表。

### 3.2 用户 Agent 配置、创建和管理

每个用户在个人设置中选择一个独立的 Agent 好友账号。Web 将该账号写入用户资料
`User.Ex` 的通用 `agent` 命名空间；它不是 Agent 工作区专用字段，后续 Agent 配置也在
同一对象中扩展：

```json
{
  "agent": {
    "userID": "agent-user-id"
  }
}
```

更新时必须解析并合并既有 `Ex`，保留其他顶层字段和 `agent` 下的未来字段。首发只使用
`userID`，不在用户或群扩展中保存 provider、provider session ID、token 或本地路径。

“新对话”不立即创建群：它先打开本地草稿页。用户首次发送非空文本时，Web 读取已配置的
Agent `userID`，创建群并发送该文本；群名取输入内容的规范化前缀（最多 36 个字符，超出以
`...` 截断）。未发送或离开草稿页不会创建群。实际创建操作为：

```text
groupType = WorkingGroup
groupName = 新对话
groupInfo.ex = {"abd":{"kind":"agent_workspace","version":1}}
memberUserIDs = [configuredAgentUserID]
sendMessage = false
```

建群参数使用好友记录中的 OpenIM `userID`。首发不新增 Agent 发现接口，也不把本机 CLI
profile 或手机号硬编码到 Web。没有已配置 Agent 账号时，“新对话”提示用户先在个人设置中
完成选择。

原型中的会话操作映射到现有能力：

| 工作区操作 | OpenIM 数据 |
| --- | --- |
| 新对话 | 首次发送时，以用户设置中的 Agent 账号创建包含双方的二人群 |
| 重命名 | 修改 `groupName`，保留 `Ex` |
| 置顶 | 当前用户的 conversation `isPinned` |
| 分享 | 邀请成员加入群 |

首发不实现归档、取消归档或永久删除。Web 保留原型中的归档按钮作为禁用占位，但点击不能
修改任何数据，也不能调用 `/conversation/delete_conversations`、
`deleteConversationAndDeleteAllMsg` 或 provider archive API。归档的用户级状态、归档列表
以及 provider thread 同步留到后续独立设计。

## 4. Web 会话路由

Web 不根据 `conversationID` 前缀、群名、成员数或首条消息猜测工作区类型。

启动和群资料同步后，从 SDK 已加入群列表构建分类索引：

```ts
type ConversationKind = "chat" | "agent_workspace";

function conversationKind(groupEx: string): ConversationKind {
  // 结构化解析；缺失、非法或未知值返回 chat。
}
```

界面分流：

```text
消息侧边栏 = conversations - agentWorkspaceConversationIDs
Agent 侧边栏 = conversations 与 agentWorkspaceConversationIDs 的交集

/chat/:conversationID  -> 普通 ChatPage
/agent/:conversationID -> AgentWorkspacePage
```

收到群资料新增、更新或删除通知后重新计算对应 ID。会话已到达但群资料尚未加载时保留
loading 状态并通过 `GetGroupsInfo` 补取，避免先错误渲染为普通聊天再跳转。

群列表只用于构建类型索引，Agent 侧边栏的数据源仍是当前 conversation 列表。

工作区必须使用独立的页面状态和 renderer。普通 `StreamMessageRender` 不增加 Agent
布局分支；Agent 页面根据 `streamElem.type` 选择 `AgentRunRenderer`。

## 5. Agent Run Stream Message

### 5.1 一条消息对应一次 run

普通群聊：

```text
StreamMsgElem.type = "text"
内容 = 最终回答增量
```

Agent 工作区：

```text
StreamMsgElem.type = "agent_run_v1"
content = Agent Run 元数据 JSON
packets = 结构化 RunEvent JSON 数组
end = run 已结束
```

同一工作区的下一次用户提问创建新的 Stream Message。单条流消息不能跨越多轮对话。

初始 `content` 示例：

```json
{
  "schema": 1,
  "runId": "run_123",
  "status": "running"
}
```

### 5.2 Packet 协议

每个 packet 是一个完整 JSON 对象，packet 顺序由现有 `startIndex` 保证：

```json
{
  "version": 1,
  "kind": "activity.summary",
  "text": "已分析题图和受力关系"
}
```

首发固定事件集合：

| `kind` | 主要字段 | 原型展示 |
| --- | --- | --- |
| `run.queued` | `runId` | 排队中 |
| `run.started` | `runId` | 正在处理 |
| `activity.summary` | `text` | 可折叠活动标题 |
| `tool.started` | `callId`, `name`, `summary` | 运行中的工具行 |
| `tool.completed` | `callId`, `status`, `durationMs`, `summary` | 完成的工具行 |
| `approval.requested` | `requestId`, `name`, `summary`, `choices` | 等待用户审批 |
| `approval.resolved` | `requestId`, `decision` | 审批结果 |
| `answer.delta` | `text` | 最终回答增量 |
| `artifact` | `name`, `mediaType`, `size`, `attachmentId` | 文件条目 |
| `run.completed` | 无 | 完成状态 |
| `run.failed` | `summary` | 有限错误说明 |
| `run.cancelled` | 无 | 已取消状态 |

`callId` 只在当前 run 内关联 `tool.started` 和 `tool.completed`。前端通过 reducer 更新同一
工具行，不需要修改已经发送的 packet。

`answer.delta` 必须在 CLI 中按 50 至 100 ms 或合适字节数合并，不能把每个 provider
token 发送到 MQ。工作区只保存 summary 和必要结果后，应保持在现有单条流消息
128 KiB、4096 packets 和 10 分钟空闲窗口内。超过限制时截断活动详情并保留最终回答；
当前实现把活动详情限制为 32 KiB，并额外为回答和终态预留 72 KiB。长任务是否延长空闲
窗口在出现真实需求后单独处理。

### 5.3 前端 reducer

`AgentRunRenderer` 将 append-only packets 归并为原型需要的状态：

```ts
type AgentRunView = {
  status: "queued" | "running" | "waiting_approval" | "completed" | "failed" | "cancelled";
  activitySummary?: string;
  tools: Map<string, ToolView>;
  approvals: Map<string, ApprovalView>;
  answer: string;
  artifacts: ArtifactView[];
};
```

渲染对应原型中的：

- `.agent-run-status`；
- `.turn-activity` 和 `.turn-activity-details`；
- Agent 最终正文和列表；
- `.agent-file-list`。

未知 packet 必须忽略，已识别的 `answer.delta` 仍正常展示。JSON 非法时显示有限的“不支持
的运行记录”状态，不能把原始 JSON 直接输出到页面。

### 5.4 取消和审批控制

用户输入仍是普通群消息。取消和审批响应不是聊天内容，不能写入历史、未读、会话预览或
离线推送。候选方案是向该工作区的独立 Agent 账号发送 online-only custom message，而不是
发送到工作区群，并采用固定命名空间 `abd.agent.control.v1`：

```json
{
  "version": 1,
  "conversationId": "sg_group_123",
  "runId": "run_123",
  "action": "cancel"
}
```

审批响应在同一 envelope 中使用 `action = "approval.respond"`，并增加 `requestId` 和固定
枚举 `decision`。CLI 只接受工作区群成员发送、且匹配当前 conversation、当前活动 run 和
待处理 request 的控制事件；重复、过期或不匹配的事件必须忽略。

该方案实施前必须用用户和 Agent 两个独立账号验证：online-only custom message 能到达 Agent
CLI 登录，且不会落历史、增加未读、改变会话预览或触发离线推送。还必须确定刷新后可靠取得
工作区 Agent `userID` 的方式。任何一项不满足时停止实现并重新选择显式 Web-to-CLI 控制通道，
不能退化为持久化 custom message。工作区启用交互审批后，Codex adapter 不再自动批准对应
请求；普通群聊保持现有行为。

当前仓库没有完成上述独立账号投递验证。因此 Web 暂不发送控制消息，取消按钮隐藏，审批
选项禁用，CLI 也不解析 `abd.agent.control.v1`。在 T005 完成前不得开启这些入口。

## 6. `abd-im-cli` 改动

### 6.1 会话分类

处理读群入站消息时，根据 `groupID` 查询现有 OpenIM `GetGroupsInfo`，解析 `GroupInfo.ex`。
可以按 `groupID` 做进程内缓存；缓存失败只影响展示模式，不能扩大 Agent 权限。

```text
agent_workspace -> Agent Run stream writer
其他会话        -> 现有 text stream writer
```

当前 group server client 已能取得完整 `sdkws.GroupInfo`，但 `internal/service/group.Group`
领域模型没有暴露 `Ex`。首发应在 daemon 内增加一个只返回分类的内部查询，避免把未经约束的
群扩展暴露给 Agent 工具。

### 6.2 Provider 输出

保留现有 `TurnRequest.Output` 和文本回复路径，增加一个可选、固定类型的 activity sink。
不要为此引入通用插件事件总线。

```go
type TurnActivity struct {
    Kind       string
    CallID     string
    Name       string
    Summary    string
    Status     string
    DurationMS int64
}

type TurnRequest struct {
    // 现有字段保持不变。
    Output   TurnOutputSink
    Activity TurnActivitySink
}
```

- 普通群聊不设置 `Activity`，provider 只产生现有最终文本。
- Agent 工作区设置 `Activity`，daemon 把 activity 和回答增量写入同一条
  `agent_run_v1` Stream Message。
- `run.started` 由 run manager 在任务实际出队执行时写入；stream 写入不继承 provider 的
  取消状态，以便失败或取消后仍能持久化唯一终态。
- Codex adapter 从稳定的 app-server item 生命周期提取 commentary summary 和工具摘要；
  原始 thought、完整命令输出和大型工具结果不会写入 activity。
- ACP adapter只映射 ACP 明确定义的 Agent message 和 tool call update；provider 没有提供
  reasoning summary 时不生成虚构内容。

失败、取消和正常完成都必须 append 对应终态 packet，然后以 `end=true` 结束流。最终回答
为空但存在活动记录时也要保留这条工作区消息。

### 6.3 本地 session 映射

现有 `provider_sessions` 保持不变，不增加服务端同步：

```text
profile_id + conversation_id + provider -> session_ref
```

工作区类型不进入这个 key；普通群和工作区都继续以 `conversation_id` 隔离 provider
上下文。

## 7. 无需修改的部分

- OpenIM conversation ID 生成规则；
- OpenIM SessionType 和 GroupType；
- protocol protobuf；
- Kafka 消息链路；
- Redis Stream Message 状态；
- WebSocket 推送和断线后的 Stream snapshot 对账；
- `abd-im-cli` event、reply slot、run queue 和 provider session 数据表；
- 普通群聊现有消息 renderer。

当前 Web composer 首发只发送单条文本消息。附件入口保留为禁用占位，避免把多个附件和正文
拆成多条群消息并意外启动多个 run；组合附件输入需要先定义单条消息格式后再开放。

Web 用户与 CLI Agent 使用两个独立 IM 账号。CLI 忽略所有由 Agent 自身账号发送的工作区
消息，避免 Agent 回复或工具调用回流后递归触发 run；其他群成员发送的受支持 prompt 类型
消息可以启动 run。

## 8. 上线顺序

必须先上线消费者，再上线结构化消息生产者：

1. Web 支持解析工作区群 `Ex`、独立 Agent 页面和 `agent_run_v1` renderer，但暂不创建
   工作区群。
2. `abd-im-cli` 支持群分类、结构化 Run packet 和普通群最终文本分流。
3. Web 开启个人设置中的 Agent 账号选择，并在“新对话”首次发送时创建带 Agent `Ex` 的二人群。
4. 验证新旧客户端行为后再默认开放入口。

旧 Web 的 Stream renderer 会直接拼接 `content + packets`。如果先启用生产者，旧页面会
显示 JSON 原文，因此不能颠倒前两步。

## 9. 验收标准

- 用户在个人设置中选择一个 Agent 好友，账号保存于 `User.Ex.agent.userID`，且更新保留
  其他扩展字段。创建 Agent 新对话仅打开草稿；首次发送才新增一个包含双方、带指定
  `GroupInfo.ex` 的普通工作群。
- 不新增 Agent 会话表、SessionType、GroupType 或实时连接。
- 刷新和重新登录后，Agent 群仍只出现在 Agent 侧边栏。
- 普通群聊仍只收到一条最终文本 Stream Message。
- Agent 工作区每次提问只产生一条 `agent_run_v1` Stream Message。
- 工作区能增量展示 activity summary、工具状态、最终回答和 artifact。
- 排队和审批状态也写入同一条 run stream；取消与审批响应不进入聊天历史或未读。
- 归档按钮可见但不可执行，不产生 SDK、服务端或 provider 调用。
- 一次 run 只产生一个会话列表项和一次未读语义，packet 不单独计入未读。
- 消息、群 `Ex` 和服务端数据库均不包含外部 Agent session ID。
- Provider 不提供 reasoning summary 时，界面只展示工具摘要和最终回答。
- 取消、失败和成功均有明确终态，断线重连后能从 Stream snapshot 恢复相同界面。
- 未识别或非法群 `Ex` 不会进入 Agent 工作区，也不会改变任何权限。
