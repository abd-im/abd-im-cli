# Agent Run Stream Protocol v2

状态：Proposed

日期：2026-08-23

## 1. 决策

Agent 工作区使用 `StreamMessage(type="agent_run_v2")` 持久化一次 Agent run。所有 Provider
在 CLI 中映射为同一套 ABDIM Canonical Event，Web 只解析这一种格式。

Canonical 能力必须至少被 Codex app-server、ACP、Hermes、DeepSeek Harness 四者之一支持。
四者都没有、只存在于其他项目的能力不纳入协议。`queued`、`truncated` 和 OpenIM packet
sequence 是 ABDIM transport 自身的必要例外。

旧 `agent_run_v1` 不兼容、不迁移。ACP 仍可作为 Provider 协议，但不直接作为 OpenIM wire
format。

## 2. 能力取舍

表中每行只比较同一种能力。`-` 表示没有对应的一等能力。OpenHands 和 Multica 仅用于横向
对照，不作为 Canonical 能力来源。

### 2.1 内容事件

| ABDIM Canonical | Codex app-server v2 | ACP v1 | ACP v2 alpha | Hermes | DeepSeek Harness | OpenHands | Multica |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Stable item ID | ThreadItem ID | Message ID 可选；Tool ID | Message/Reasoning/Tool ID | ACP Tool ID | Tool ID；event seq | Event ID | Tool CallID |
| Message + phase | AgentMessage phase/delta | Agent message chunk | Message state/patch | Message callback | assistant chunk/message | Message | text |
| Reasoning summary | Reasoning summary delta | Agent thought chunk | Reasoning state/patch | Thinking callback | - | Reasoning content | thinking |
| Tool lifecycle | Item started/delta/completed | tool call/update | Tool state/patch | Tool start/update | tool call/result | Action/Observation | tool-use/result |
| Text/image/resource | Text/image output | ContentBlock | Structured content | Text/image/resource | Message/tool result | Observation content | text/tool output |
| Terminal output | CommandExecution | Terminal block | Structured terminal | Process result | Tool result | Cmd output | Tool output |
| File diff/location | FileChange | Diff/location | Structured diff | Patch/location | Tool metadata | File observation | Tool I/O |
| Plan snapshot | TurnPlan | Plan update | Identified plan | Todo plan update | todo/write | Goal state | - |
| Usage snapshot | Token/context usage | Usage update | Usage state | Token accounting | Assistant usage | Conversation stats | Model usage |
| Exact cost | - | - | - | actual_cost_usd | - | LLM metrics | Provider cost |
| Permission | Approval request | Permission RPC | Permission subject | Command/edit approval | - | Confirmation | - |
| Artifact reference | Image generation/view | Image/resource link | Structured resource | Image/resource tool | - | File artifact | - |
| Subagent lifecycle | CollabAgent | - | Background work | delegate_task | Subagent notification | Delegation event | - |

### 2.2 生命周期与控制

| ABDIM Canonical | Codex app-server v2 | ACP v1 | ACP v2 alpha | Hermes | DeepSeek Harness | OpenHands | Multica |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Run started/finished | Turn lifecycle | Prompt boundary | Running/idle state | Prompt/session state | turn start/end | Conversation state | Final Result |
| Cancel preserves prefix | Interrupt + persisted delta | 已发送 chunk | Identified content state | Interrupted transcript | Interrupted prefix | Durable events | Aborted messages |
| Deterministic replay | Thread read/resume | Session load/resume | Session state | SessionDB replay | Append-only event log | EventLog replay | Session resume |
| Session control separate from events | Thread RPC/events | Session RPC/update | RPC/state update | ACP/SessionDB | RPC/event map | Control/events | Daemon/messages |
| Cancel/steer | Interrupt/steer | Cancel | Cancel/background work | Cancel/steer/queue | Admission/quiescence | Pause/interrupt | Cancel/timeout |

取舍结果：Canonical 定义 message、reasoning、tool、plan、artifact、agent、usage、permission 和
run lifecycle。Provider 的 session list/load/fork、mode、config、raw log、hook、checkpoint 等
不属于单次 run stream。

## 3. Stream Envelope

每次用户提问创建一条 Stream Message：

```json
{
  "streamType": "agent_run_v2",
  "content": {
    "schema": "abd.agent_run",
    "schemaVersion": 2,
    "runId": "run_0193...",
    "triggerMessageId": "msg_0193..."
  }
}
```

`content` 只包含不可变元数据。运行状态全部来自 packets。Packet 不重复携带 `runId` 或
sequence；OpenIM `startIndex` 就是 canonical sequence。

## 4. RunEvent

所有 packet 都包含：

```ts
type EventBase = {
  event: string;
  at: number; // Unix milliseconds
  _meta?: Record<string, unknown>; // provider namespace only
};
```

事件集合：

| `event` | Payload | 语义 |
| --- | --- | --- |
| `run.queued` | - | Stream 已创建，run 尚未执行 |
| `run.started` | - | Run manager 开始执行 |
| `run.truncated` | `reason: "size_limit"` | 用户可见内容因 transport 上限被截断 |
| `item.started` | `item: RunItem` | 创建 item |
| `item.delta` | `itemId`, `content: ContentBlock` | 向 message/reasoning/tool 追加内容 |
| `item.updated` | `itemId`, `update: ItemUpdate` | 替换 item 的可变状态 |
| `item.completed` | `itemId`, `outcome`, `errorCode?` | 终结 item |
| `usage.updated` | `usage: Usage` | Run 累计用量 snapshot |
| `permission.requested` | `request: PermissionRequest` | 创建待处理授权 |
| `permission.resolved` | `resolution: PermissionResolution` | 终结授权 |
| `run.finished` | `outcome`, `reason`, `errorCode?`, `durationMs?` | 终结 run |

示例：

```json
{"event":"run.started","at":1787472000000}
```

```json
{
  "event": "item.started",
  "at": 1787472000100,
  "item": {
    "id": "message_1",
    "type": "message",
    "role": "assistant",
    "phase": "final",
    "content": []
  }
}
```

```json
{
  "event": "item.delta",
  "at": 1787472000200,
  "itemId": "message_1",
  "content": {"type":"text","text":"正在检查相关文件。"}
}
```

```json
{"event":"item.completed","at":1787472001000,"itemId":"message_1","outcome":"completed"}
```

```json
{"event":"run.finished","at":1787472001100,"outcome":"completed","reason":"end_turn"}
```

## 5. RunItem

所有 item 都有 `id` 和 `type`。Provider 没有稳定 ID 时由 adapter 生成 UUID，同一逻辑 item
的后续事件必须复用。

| `type` | 字段 |
| --- | --- |
| `message` | `role: "assistant"`, `phase: "commentary/final"`, `content: ContentBlock[]` |
| `reasoning` | `content: ContentBlock[]`；仅保存公开 summary |
| `tool` | `name`, `title`, `category`, `status`, `input?`, `content`, `locations`, `durationMs?`, `errorCode?` |
| `plan` | `entries: {id,title,status}[]` |
| `artifact` | `name`, `mediaType`, `uri?`, `attachmentId?`, `size?` |
| `agent` | `name?`, `task?`, `childRunId?`, `status`, `summary?` |

枚举：

- Tool category：`execute/read/edit/search/fetch/mcp/other`
- Tool status：`pending/running/completed/failed/cancelled/declined`
- Plan status：`pending/in_progress/completed/failed/skipped`
- Agent status：`pending/running/completed/failed/cancelled`
- Item outcome：`completed/failed/cancelled/declined`

`item.updated.update` 是 sealed union，不使用 JSON Patch：

| `update.type` | Snapshot |
| --- | --- |
| `tool.state` | `title`, `status`, `locations`, `durationMs?`, `errorCode?` |
| `plan.entries` | 完整 `entries` |
| `agent.state` | `childRunId?`, `status`, `summary?` |

## 6. ContentBlock

| `type` | 字段 |
| --- | --- |
| `text` | `text` |
| `image` | `uri?`, `attachmentId?`, `mediaType`, `alt?` |
| `resource` | `uri?`, `attachmentId?`, `name`, `mediaType?`, `size?` |
| `diff` | `path`, `oldText`, `newText`, `truncated?` |
| `terminal` | `command`, `output`, `exitCode?`, `truncated?` |

Image/resource 的 `uri` 与 `attachmentId` 至少存在一个。未知 Provider content 必须降级为已有
block，或者放入 namespaced `_meta` 并由通用 UI 忽略。

## 7. Usage 与 Permission

Usage 是累计 snapshot；没有数据的字段省略：

```ts
type Usage = {
  inputTokens?: number;
  cachedInputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
  cost?: { currency: string; amount: string };
};
```

`cost` 只接受 Provider 报告的实际费用。Hermes `actual_cost_usd` 可以映射，estimated cost 不
进入 canonical 字段。

```ts
type PermissionRequest = {
  id: string;
  itemId?: string;
  title: string;
  description?: string;
  options: { id: string; kind: string; label: string }[];
};

type PermissionResolution =
  | { requestId: string; outcome: "selected"; optionId: string }
  | { requestId: string; outcome: "cancelled"; reason: string };
```

## 8. Lifecycle

```text
queued -> started -> finished
queued -----------> finished
```

- 一条 run 恰好一个 `run.queued`、最多一个 `run.started`、恰好一个 `run.finished`。
- 同一 item 恰好一个 started、最多一个 completed；completed 后不得更新。
- `run.finished` 前必须终结所有已落盘 item 和 permission。
- Writer 串行 append；重试使用相同 `startIndex` 和相同 packet。
- `run.finished` 与 `streamElem.end=true` 同次写入；`end` 不能替代 terminal event。
- 未知 optional 字段忽略；未知 event/item/content 或 major version 不猜测解析。

## 9. Control Message

Run stream 只记录事实。审批和取消通过同一 workspace 内的 OpenIM Custom Message 发送：

```json
{
  "schema": "abd.agent_run_control",
  "schemaVersion": 1,
  "action": "permission.resolve",
  "runId": "run_0193...",
  "requestId": "permission_1",
  "outcome": "selected",
  "optionId": "allow_once"
}
```

```json
{
  "schema": "abd.agent_run_control",
  "schemaVersion": 1,
  "action": "run.cancel",
  "runId": "run_0193..."
}
```

`CustomElem.description` 固定为 `abd.agent_run_control`。Daemon 只接受同 conversation、原提问者、
仍 active run 的有效命令。Control message 不直接改变 Web reducer；后续
`permission.resolved`/`run.finished` 才是权威状态。

## 10. Provider Mapping

| Provider source | Canonical |
| --- | --- |
| Agent message/chunk | message started/delta/completed |
| Reasoning/thinking summary | reasoning started/delta/completed |
| Tool call/result | tool started/delta/updated/completed |
| Plan/todo | plan started/updated/completed |
| Usage | usage.updated |
| Approval/permission | permission requested/resolved |
| Image/resource output | ContentBlock 或 artifact item |
| Collab/background/subagent | agent started/updated/completed |
| Provider stop reason | daemon 映射为 run.finished |

Provider session initialize/list/load/resume、mode、config、checkpoint 和原生 event log 留在 adapter，
不写入 run stream。

## 11. Transport 与范围

- 单 packet `<16 KiB`，单次 append `<64 KiB`，单条 stream `<128 KiB`，最多 4096 packets。
- Adapter 负责合并过细 delta、拆包、截断、脱敏；Writer 不产生半个 JSON/ContentBlock。
- 不保存 raw chain-of-thought、凭据、环境变量、无限终端输出或 Provider 调试日志。
- 达到容量上限时保留 final message 和 terminal state，并写一次 `run.truncated`。

只修改：

| 项目 | 职责 |
| --- | --- |
| `abd-im-cli` | Canonical types、Provider adapter、OpenIM writer、control handler |
| `abd-im-web` | Parser、reducer、renderer、control sender |

现有 server/SDK 已透明传输 Stream Message 和 Custom Message，不需要修改。
