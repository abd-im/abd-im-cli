---

description: "Implementation tasks for the OpenIM Agent workspace"

---

# Tasks: OpenIM Agent 工作区

**Input**: [`docs/AGENT_WORKSPACE_DESIGN.md`](./AGENT_WORKSPACE_DESIGN.md) and the prototype at
`/home/me/code/abd-im-web/docs/agent-workbench/index.html`

**Scope**: Reuse OpenIM group conversations. Do not add an Agent conversation table, protocol
type, GroupType, SessionType, Kafka/Redis stream, or separate WebSocket.

**MVP non-goal**: Do not implement archive, unarchive, permanent deletion, an archive list, or
provider thread archive synchronization. Keep the prototype's archive button as a disabled
placeholder; do not wire it to `delete_conversations`, `deleteConversationAndDeleteAllMsg`, or a
provider API.

**Tests**: Tests are included because the change crosses the Web, SDK event, stream message, and
provider boundaries.

## Phase 0: Existing Prerequisite

**Purpose**: Keep the dependency release prerequisite visible.

- [ ] T001 Publish an `abd-im-sdk-core` version containing the Stream API; remove the local
  `replace` in `go.mod`, verify an independent checkout resolves the published module, and run
  the CLI test suite.

## Phase 1: Foundational Contracts

**Purpose**: Freeze shared contracts before Web and provider work starts.

- [x] T002 [P] Add focused parser tests for Agent group classification in
  `internal/service/group/agent_workspace_test.go`. Cover missing/invalid/unknown `GroupInfo.ex`,
  valid workspace `GroupInfo.ex`, and unrelated extension fields.
- [x] T003 [P] Specify and test the `agent_run_v1` stream envelope and packet schema in
  `internal/reply` tests: `run.queued`, `run.started`, `activity.summary`, `tool.started`,
  `tool.completed`, `approval.requested`, `approval.resolved`, `answer.delta`, `artifact`,
  `run.completed`, `run.failed`, and `run.cancelled`.
- [x] T004 Define the narrow activity and workspace-classification interfaces in
  `internal/contracts/contracts.go`, implement the group marker parser in
  `internal/service/group/agent_workspace.go`. Keep the existing text `TurnOutputSink` contract
  unchanged.
- [ ] T005 Verify that a namespaced, online-only custom message from the Web user reaches the
  selected Agent account's active CLI login without history, unread, preview, or offline push side
  effects. Record the result in `docs/AGENT_WORKSPACE_DESIGN.md`; if it fails, stop and choose an
  explicit Web-to-CLI control transport before T022/T024/T028.

**Checkpoint**: The JSON shapes and fallback behavior are testable without a running provider.

## Phase 2: User Story 1 - Agent Workspace Conversation Lifecycle (Priority: P1)

**Goal**: Create, classify, route, rename, pin, and share an Agent workspace using the existing
group and conversation records.

**Independent Test**: Create a workspace group, reload/re-login, rename, pin, and share it, then
verify the same `conversationID` and `GroupInfo.ex` remain available and the group never appears in
the ordinary message list.

### Tests for User Story 1

- [x] T006 [US1] Add Vitest and the minimal Vite-compatible test configuration to
  `/home/me/code/abd-im-web/package.json`, then add unit tests for parsing/merging `GroupInfo.ex`
  beside
  `/home/me/code/abd-im-web/src/features/agentWorkspace/metadata.ts`.
- [x] T007 [US1] Add Web store tests for ordinary versus Agent conversation lists and group
  metadata changes in `/home/me/code/abd-im-web/src/store/conversation.test.ts`.
- [x] T008 [P] [US1] Add CLI tests for workspace metadata lookup and failure fallback in
  `internal/daemon/inbound_test.go` and focused group-source tests. The MVP performs a narrow
  lookup per inbound event and does not add a cache.

### Implementation for User Story 1

- [x] T009 [US1] Add a narrow daemon-internal group classification query using the existing OpenIM
  group source in `internal/service/group/openim.go` and `internal/daemon/inbound.go` (or a focused
  sibling). Return only `chat` or `agent_workspace`; do not expose arbitrary `GroupInfo.ex` to Agent
  tools.
- [x] T010 [US1] Add Web handling for group metadata loading and refresh so a conversation remains
  in a loading state until its workspace kind is known and does not briefly render as normal chat,
  using `/home/me/code/abd-im-web/src/store/conversation.ts` and
  `/home/me/code/abd-im-web/src/layout/useGlobalEvents.tsx`.
- [x] T011 [US1] Add Web Agent metadata helpers and conversation actions in
  `/home/me/code/abd-im-web/src/features/agentWorkspace/metadata.ts` and
  `/home/me/code/abd-im-web/src/features/agentWorkspace/actions.ts`: store the selected Agent
  account in `User.Ex.agent.userID`, create a two-member WorkingGroup with `GroupInfo.ex` only on
  the first draft submission, preserve unrelated JSON fields while renaming, set pin, and invite
  members.
- [x] T012 [US1] Split the Web conversation index into normal and Agent views in
  `/home/me/code/abd-im-web/src/store/conversation.ts`,
  `/home/me/code/abd-im-web/src/layout/useGlobalEvents.tsx`, and the relevant sidebar components.
  A group metadata change must move the item to the correct view without changing unread counts.
- [x] T013 [US1] Add the Agent workspace sidebar and its rename/pin/share controls in
  `/home/me/code/abd-im-web/src/pages/agent`; render the archive action as a disabled placeholder
  with no SDK or provider side effect.
- [x] T014 [US1] Add browser integration coverage in `/home/me/code/abd-im-web/e2e/` for create,
  classify, reload, rename, pin, and share.

## Phase 3: User Story 2 - Separate Agent Workspace Rendering (Priority: P1)

**Goal**: Render Agent runs in a dedicated page and reducer while ordinary group chat keeps its
  existing message renderer and list semantics.

**Independent Test**: Feed an `agent_run_v1` snapshot containing activity, tool updates, answer
  deltas, an artifact, and a terminal status; verify the Agent page renders it while a normal chat
  renders only its final text.

### Tests for User Story 2

- [x] T015 [P] [US2] Add reducer tests for run/approval/terminal transitions, tool updates,
  activity summaries, unknown packets, malformed JSON, and ended-stream fallback in
  `/home/me/code/abd-im-web/src/pages/agent/agentRunReducer.test.ts`.
- [ ] T016 [P] [US2] Add Web route/navigation tests for `/agent/:conversationID`, ordinary
  `/chat/:conversationID`, loading while group metadata is missing, and fallback to normal chat
  for invalid `GroupInfo.ex` in `/home/me/code/abd-im-web/src/routes/index.test.tsx`.
- [ ] T017 [P] [US2] Add stream rendering regression tests proving one run creates one conversation
  preview/unread event and packets do not become separate chat messages in
  `/home/me/code/abd-im-web/src/pages/chat/queryChat/MessageItem`.

### Implementation for User Story 2

- [x] T018 [US2] Create the Agent run reducer and renderer in
  `/home/me/code/abd-im-web/src/pages/agent/AgentRunRenderer.tsx` and adjacent files. Render
  summary reasoning, tool status/duration, approval requests/results, artifacts, final answer,
  cancellation, failure, and completion; ignore unknown packets and never print raw JSON.
- [x] T019 [US2] Add the dedicated Agent page state, text composer, run status, history loading,
  and conversation selection in `/home/me/code/abd-im-web/src/pages/agent/AgentWorkspaceContent.tsx`.
- [x] T020 [US2] Update `/home/me/code/abd-im-web/src/routes/index.tsx`,
  `/home/me/code/abd-im-web/src/layout/MainContentLayout.tsx`, and
  `/home/me/code/abd-im-web/src/layout/LeftNavBar/index.tsx` to route Agent conversations to the
  dedicated page without adding Agent branches to the normal chat renderer.
- [x] T021 [US2] Keep
  `/home/me/code/abd-im-web/src/pages/chat/queryChat/MessageItem/StreamMessageRender.tsx` unchanged;
  route workspace conversations before rendering so `AgentRunRenderer` owns all workspace layout
  and the existing `text` stream behavior remains untouched.
- [ ] T022 [US2] Implement run controls after T005 validates the control path: send cancellation to
  the selected Agent account as a namespaced online-only custom message containing
  `conversationID`, `runId`, and `cancel`, and send approval responses with the bound `requestId`
  and fixed decision enum; include running,
  queued, approval, cancelled, failed, and completed visual states from the prototype. Attachment
  composition remains separately deferred until one user turn can be represented by one message.

## Phase 4: User Story 3 - Structured Agent Run Stream (Priority: P1)

**Goal**: Send one bounded structured stream message per Agent turn while ordinary chats continue
  to receive only final text.

**Independent Test**: Send one prompt to an Agent workspace and observe exactly one stream message
  with ordered packets and one terminal packet; send the same prompt to a normal group and observe
  the existing final-text stream only.

### Tests for User Story 3

- [x] T023 [P] [US3] Add focused Agent Run stream tests for envelope initialization, packet
  ordering, terminal packets, activity mapping, packet splitting, and byte-budget preservation in
  `internal/reply/agent_run_test.go`.
- [ ] T024 [P] [US3] Add inbound routing tests for Agent versus normal group classification,
  authenticated namespaced cancellation, stale/wrong-run cancellation,
  approval response binding, provider failure, and duplicate inbound message idempotency in
  `internal/daemon/inbound_test.go`.
- [x] T025 [P] [US3] Add bridge delivery tests proving `agent_run_v1` uses one
  `StreamDelivery`/`AppendStreamMessage` sequence and normal text remains unchanged in
  `internal/bridge/abdim/adapter_test.go`.

### Implementation for User Story 3

- [x] T026 [US3] Add the bounded `agent_run_v1` writer and packet reducer-facing envelope in
  `internal/reply/stream.go` (or a focused sibling). Enforce the existing 16 KiB packet, 128 KiB
  message, packet-count, and idle-window limits; truncate activity details before final answer data.
- [x] T027 [US3] Update `internal/daemon/inbound.go` to classify the group from `GroupInfo.ex`,
  select the Agent run writer only for workspaces, and preserve the existing text writer for normal
  groups. Emit `run.queued` when applicable, `run.started` on execution, and exactly one terminal
  packet for every outcome.
- [x] T028 [US3] Extend `internal/bridge/abdim/adapter.go` and its delivery types so structured
  packets are appended through the existing Stream API with stable `startIndex` ordering and
  reconnect snapshot compatibility. The cancellation/approval schema remains blocked by T005 and
  is tracked by T022/T024/T036.
- [x] T029 [US3] Keep `internal/contracts/contracts.go` backward compatible while wiring the
  optional activity sink through the turn request. Ordinary turns must leave the sink nil.

## Phase 5: User Story 4 - Provider Activity (Priority: P1)

**Goal**: Convert provider lifecycle updates into summaries/tool packets without leaking external
session IDs to IM.

**Independent Test**: A Codex run shows tool start/completion, approvals, and answer deltas, can be
cancelled, and continues using the same local session reference on the next turn.

### Tests for User Story 4

- [ ] T030 [P] [US4] Add Codex adapter tests for stable item lifecycle mapping, commentary summary,
  tool start/completion, interactive approval request/response, answer delta coalescing,
  cancellation, and session resume in `internal/agent/provider/codex/codex_test.go`.
- [ ] T031 [P] [US4] Add ACP adapter tests for supported agent/tool updates, missing reasoning
  summary fallback, cancellation, and session resume in `internal/agent/provider/acp/acp_test.go`.
- [ ] T032 [P] [US4] Add session-store tests proving workspace runs keep using the existing
  `(profile_id, conversation_id, provider) -> session_ref` mapping in `internal/control/store_test.go`.

### Implementation for User Story 4

- [x] T033 [US4] Map Codex app-server `item/started`, `item/completed`, commentary, tool, and
  answer events to the fixed `TurnActivity`/run packet contract in
  `internal/agent/provider/codex/codex.go`; do not emit raw Chain-of-Thought or full tool logs.
  Interactive approvals remain blocked by T005 and are tracked by T036.
- [x] T034 [US4] Preserve Codex session resume and missing-session fallback while adding structured
  activity output in `internal/agent/provider/codex/codex.go`. Keep session refs local and never
  include them in stream metadata or packets.
- [x] T035 [US4] Map only ACP-defined message and tool-call updates in
  `internal/agent/provider/acp/acp.go`; do not fabricate reasoning summaries or expose provider
  session IDs.
- [ ] T036 [US4] Wire queue, cancellation, and approval state between the run manager, provider,
  structured stream writer, and validated control-message path.

## Phase 6: Compatibility, Verification, and Release

**Purpose**: Validate old clients and operational limits before enabling workspace creation.

- [ ] T037 [P] Add end-to-end stream compatibility tests across CLI and Web fixtures for reconnect,
  packet duplication, malformed packet, run cancellation, and terminal-state recovery.
- [x] T038 [P] Add a migration/fixture test proving existing groups with absent or unrelated
  `GroupInfo.ex` remain ordinary chats and unrelated group extension fields are preserved.
- [ ] T039 Run `go test ./...` in `/home/me/code/abd-im-cli`, `pnpm lint`, `pnpm build:web`,
  and the targeted Playwright suite in `/home/me/code/abd-im-web`; record failures and environment
  prerequisites in the implementation PR.
- [ ] T040 Deploy the Web consumer and renderer before enabling structured producers; then enable
  CLI routing/providers; only after old-client verification enable the Agent “新对话” entry point.
- [x] T041 Update `docs/AGENT_WORKSPACE_DESIGN.md` and this task list to record the implemented
  user Agent configuration, draft lifecycle, stream behavior, and deferred control validation.

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 0** is an existing SDK release prerequisite and can be completed independently.
- **Phase 1** blocks all implementation stories because it freezes the metadata, stream, and
  activity contracts.
- **User Stories 1 and 2** can proceed in parallel after Phase 1; both are Web-heavy but touch
  different state/rendering boundaries.
- **User Story 3** depends on T003/T004 and is required before provider event integration can be
  demonstrated end to end.
- **User Story 4** depends on T027-T029 and the group classifier from US1.
- **Phase 6** depends on the desired P1 stories.
- **T005** specifically blocks cancellation controls T022/T024/T036; do not infer that an
  online-only user-to-Agent message has the required non-history delivery behavior.

### Parallel Opportunities

After Phase 1, assign separate agents to:

- **Web lifecycle**: T006-T014 in `/home/me/code/abd-im-web`;
- **Web renderer**: T015-T022 in `/home/me/code/abd-im-web`;
- **CLI stream**: T023-T029 in `/home/me/code/abd-im-cli`;
- **Providers**: T030-T036 after the stream contract is stable.

Do not parallelize edits to the same contract files until T004 is reviewed. T033 and T036 must be
coordinated so approval or cancellation cannot race a provider turn.

## Acceptance Criteria

T005/T022/T024/T036 是启用交互取消和审批前的发布门槛；当前基础链路的验收不把这些未验证
控制能力视为已完成。

- Agent and ordinary groups reuse the same OpenIM conversation ID space.
- The Web user and CLI Agent are independent IM accounts; creating a workspace selects one Agent
  friend and includes that Agent `userID` in the group.
- Agent classification comes only from structured `GroupInfo.ex`; invalid metadata falls back to
  ordinary chat.
- A normal turn emits the existing final-text stream; an Agent turn emits exactly one bounded
  `agent_run_v1` stream message.
- Activity summaries, tool states, artifacts, final answer, cancellation, failure, and completion
  render only in the Agent workspace page.
- Queue and approval state are persisted as packets in the same run message; approval responses use
  the validated non-history control path and are bound to the active request.
- Run packets do not create extra unread counts, previews, notifications, or normal-chat rows.
- Cancellation control is authenticated, scoped to the active run, and creates no history,
  unread, preview, or offline push entry.
- Provider session references remain local and do not appear in IM metadata or stream packets.
- The archive control remains a disabled placeholder and produces no SDK, server, or provider call.
- Unknown packets, unsupported provider activity, reconnects, and malformed metadata have defined
  fallback behavior.

## Deferred Work

- [ ] Other Agent ACP integrations beyond the fixed provider adapters, including independent
  launch/CLI/output/cancel tests.
- [ ] Reversible archive/unarchive, including user-level state, archive list, and provider thread
  archive synchronization.
- [ ] A separate permanent-delete or server-side retention policy, if product requirements later
  distinguish it from reversible archive.

## Verification Record

2026-08-08 本地验证：

- `go test ./...` 通过；
- `go test -race ./internal/reply ./internal/daemon ./internal/agent/run
  ./internal/agent/provider/codex ./internal/agent/provider/acp` 通过；
- Web `pnpm test` 通过，共 4 个测试文件、18 个测试；
- Web `pnpm build:web` 通过，保留仓库现有的 Vite/Ant Design 与大 chunk 警告；
- 本次新增 Agent 页面定向 ESLint 无错误或文件级警告；全仓 `pnpm lint` 仍有既有错误，未在
  本功能中清理；
- Playwright 能发现 `e2e/agent-workspace.spec.ts` 的生命周期用例；实际执行需要
  `ABD_AGENT_E2E_BASE_URL`、`ABD_AGENT_E2E_EMAIL`、`ABD_AGENT_E2E_PASSWORD`、
  `ABD_AGENT_E2E_AGENT_CONTACT` 和 `ABD_AGENT_E2E_SHARE_CONTACT`；
- T005 还需要用户与 Agent 两个独立账号的真实 OpenIM 投递验证，因此取消/审批控制保持禁用。
