# Testing

## Default

```bash
go test ./...
go vet ./...
```

`cmd/abdim` tests cover interactive ABD setup without real credentials,
owner-only token persistence, lifecycle status/stop behavior, removal of the
old manual setup commands, and the paired-owner capability policy.
`internal/daemon` tests cover exact private-message pairing, expiry, canonical
owner binding, and the guarantee that pairing traffic never starts a provider
run. `internal/connector` verifies the fixed ABD login request and sanitized
failure handling with an in-process HTTP server.

## GitHub Actions

`.github/workflows/ci.yml` runs formatting, `go vet ./...`, `go test ./...`,
the four Unix release build targets on internal pull requests and pushes to
`main`. It needs the read-only
`ABDIM_SDK_READ_TOKEN` secret because the pinned SDK module is private.

`.github/workflows/controlled-integration.yml` is manual-only and uses the
protected `openim-integration` environment. It runs the non-destructive
compatibility/profile/conversation checks plus controlled group-create and
message-send gates; it may create test groups and send a test message.

## P1 Runtime E2E

`tests/e2e/runtime_inbound_reply_test.go` and
`tests/e2e/grant_bound_message_reads_test.go` are no-credential, in-process P1
gates included in `go test ./...`. The runtime gate composes the daemon runtime
with the shared fake SDK/provider, verifies the profile lock before SDK
allocation, then verifies the ready Unix socket, callback deduplication, one
reply slot and event-bound reply. It reopens the control database and verifies
restart work is recorded as `state.reconciled`, not as a fabricated inbound
message. The grant gate composes a real run-private proxy, grant store, and
typed message service to verify policy tool selection, conversation targets,
and the after/before message window for history, search, and get.
`tests/e2e/group_create_operation_test.go` composes the real run-private
proxy, operation guard, and control database to verify the group member
allowlist, idempotency conflict behavior, and that an unknown action remains
non-retryable after reopening the database.
`tests/e2e/message_send_operation_test.go` applies the same checks to a
message target: the grant must name the user or group, repeated idempotency
keys return the recorded operation, and an unknown send cannot be rebuilt
after reopening the database.

Run it directly with:

```bash
go test ./tests/e2e -run TestRuntimeInboundReplyE2E
```

## Provider MCP E2E

`tests/e2e/provider_mcp_boundary_test.go` launches the fixed Codex adapter
against a helper process. It verifies that a run creates a fresh `CODEX_HOME`,
copies only the current user's login, does not inherit the source MCP
configuration, exposes only the allowed MCP tool snapshot through one
run-private socket, and removes the run directory after close. This validates
the normal typed provider boundary; same-user execution is intentionally not an
OS-level sandbox.

```bash
go test ./tests/e2e -run TestProviderRunPrivateMCPBoundaryE2E
```

## Run Cancellation E2E

`tests/e2e/run_cancellation_test.go` uses the event-bound inbound path to
verify that a policy change cancels an active provider, revokes its private
proxy, and suppresses its reply. A companion run-manager case verifies the
`grant_expired` terminal status and proxy closure at grant expiry.

```bash
go test ./tests/e2e -run 'Test(PolicyChangeCancelsEventBoundRunAndRevokesProxyE2E|GrantExpiryCancelsRunAndClosesProxyE2E)'
```

## Run Operations E2E

`tests/e2e/run_operations_test.go` starts the event-bound inbound path with a
durable run tracker and reaches the owner socket through local RPC. It verifies
that owner cancellation closes the run-private proxy and provider session,
suppresses the reply, and remains unavailable to provider tools. It also
checks restart recovery marks only queued/running records `interrupted`, keeps
`unknown` operations terminal, and excludes input/idempotency markers from
owner operation diagnostics.

```bash
go test ./tests/e2e -run TestOwnerRunCancellationUsesLocalRPCAndClosesProviderBoundaryE2E
```

## Compatibility Evidence

`tests/compatibility` records the fixed single-Codex provider, MCP protocol,
SDK version, and OpenIM API contract. The daemon applies that evidence to
action manifests at startup; a mismatched runtime combination downgrades every
static `available` entry to `not_validated`.

```bash
go test ./tests/compatibility
```

The controlled server probe is read-only and requires a short-lived IM token:

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`

```bash
go test -tags=integration ./tests/compatibility -run TestOpenIMServerMatchesSingleCodexCompatibilityMatrix
```

## Privacy Regression E2E

`tests/e2e/privacy_regression_test.go` injects token and inbound-body markers
through the runtime callback path. It verifies that the markers are absent from
the control database, durable ledger, owner RPC response, and reply. It also
checks the SDK, HTTP, WebSocket, stderr, and audit logging surfaces. The
run-private provider configuration test additionally verifies that source
Codex configuration markers are not inherited.

```bash
go test ./tests/e2e -run TestTokenAndInboundBodyStayOutOfVisibleBoundariesE2E
```

## OpenIM Group Integration

`internal/service/group` contains an explicit integration gate for the pinned
OpenIM SDK fork revision in `go.mod` and a compatible server. It is excluded
from default tests.

Configure these CI-secret environment variables with a pre-provisioned test
user who belongs to the named test group. `ABDIM_OPENIM_TOKEN` must be a
short-lived IM token; never use, record, or commit a browser login password.

For the current deployment, set `ABDIM_OPENIM_API_ADDR` to
`https://2.alissa.xin/api`. The SDK WebSocket address is
`wss://2.alissa.xin/msg_gateway`; it is supplied through the connector when
running the full daemon.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_GROUP_ID`
- `ABDIM_OPENIM_MEMBER_QUERY`

Run the gate with:

```bash
go test -tags=integration ./internal/service/group -run TestOpenIMGroupReadsIntegration
```

The tagged test fails if any required variable is absent. It exercises group
list/get/search and member list/search through the typed service and validates
the response schema and capability metadata. It does not create or modify data.

## OpenIM Group Create Integration

`internal/capability/group` calls the authenticated
`/group/create_group` action without using the SDK local synchronization API.
This gate creates one group, so use a disposable owner account and a distinct,
pre-provisioned disposable member. Do not use a production account.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_GROUP_CREATE_MEMBER_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/capability/group -run TestOpenIMGroupCreateIntegration
```

The test verifies the fixed server action with the authenticated user as owner
and the supplied member. Unit tests cover the manifest/grant/member allowlist
intersection and preserve an `unknown` operation when no server result can be
verified.

## OpenIM Group Membership Integration

`internal/capability/group` verifies member state through
`/group/get_group_members_info` before quit, invite, or kick. Join uses the
fixed server action directly because a non-member cannot read group members.
The gate creates a disposable working group, has the member leave, then
verifies invite, remove, and join with the two controlled accounts. The owner
group remains after the test because group dismissal is not part of the public
capability surface.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_ID`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_TOKEN`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_ID`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_TOKEN`

Run the gate with:

```bash
go test -tags=integration ./internal/capability/group -run TestOpenIMGroupMembershipIntegration
```

Unlike the older group-create gate, this test skips when its dedicated fixture
variables are absent. It uses only short-lived IM tokens and never records the
tokens, message bodies, or group member data.

## OpenIM Group Administration Integration

`internal/capability/group` reads current member roles through
`/group/get_group_members_info`, then invokes only
`/group/set_group_info_ex`, group mute/cancel-mute, member mute/cancel-mute,
and `/group/transfer_group`. The gate creates a disposable working group with
the second controlled account as a member, updates its bounded profile, toggles
both mute states, and transfers ownership last. It does not call an SDK Group
API or write local synchronized group state.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_ID`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_OWNER_TOKEN`
- `ABDIM_OPENIM_GROUP_MEMBERSHIP_MEMBER_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/capability/group -run TestOpenIMGroupAdministrationIntegration
```

The test skips without this dedicated fixture and uses only a short-lived
owner IM token. Unit and proxy tests cover field and duration bounds,
method-scoped group/user grants, role rejection, idempotency, and unknown
outcomes.

## OpenIM Message Actions Integration

`internal/capability/message` exercises the daemon-owned `UserContext`
lifecycle and the fixed server-read message source. It sends a text and quote
to a distinct controlled account, then creates a disposable controlled group
and sends one @ message to that account. The configured platform must match
the short-lived token's platform.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_WS_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_PLATFORM_ID`
- `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/capability/message -run 'TestOpenIM(TextSend|QuoteSend|TextAt)Integration'
```

The gate reaches `ready` through `InitSDK -> InitResources -> listener ->
Login` and only passes after each SDK delivery callback. Quote sends select the
source message through the fixed authenticated history endpoint; @ sends first
create a disposable controlled group containing the recipient. Unit and e2e
tests cover method-scoped grants, idempotency and unknown-outcome behavior.

## OpenIM Media Message Integration

`internal/bridge/abdim` streams daemon-held image, file, sound, and video
attachments into the narrow SDK sender. The provider supplies only opaque
attachment references and display names; the SDK receives a temporary private
upload path and cleans its cache after a confirmed callback.

Use the same six environment variables as the message action gate:

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_WS_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_PLATFORM_ID`
- `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/bridge/abdim -run TestAdapterMediaUploadIntegration
```

The gate uses generated disposable payloads and requires a confirmed SDK
callback for all four media kinds. Unit and provider MCP tests cover attachment
kind, run/grant/target binding, path rejection, idempotency, and unknown
outcomes.

## OpenIM Mark Read Integration

`internal/capability/conversation` sends three disposable controlled messages,
resolves ordered server sequence boundaries through `/msg/pull_msg_by_seq`, and
calls the fixed `/msg/mark_conversation_as_read` action. The provider-facing
method never accepts a server sequence.

Use the same six environment variables as the message action gate:

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_WS_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_PLATFORM_ID`
- `ABDIM_OPENIM_MESSAGE_SEND_RECIPIENT_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/capability/conversation -run TestOpenIMMarkReadIntegration
```

The test changes only the controlled account's read marker. Unit and e2e tests
verify grant target, finite window, idempotency and unknown-outcome semantics.

## OpenIM Profile Integration

`internal/service/profile` uses the same controlled server deployment and
three shared secrets: `ABDIM_OPENIM_API_ADDR`, `ABDIM_OPENIM_USER_ID`, and
`ABDIM_OPENIM_TOKEN`. It verifies the fixed `/user/get_users_info` mapping for
`user.me`, `user.get`, and the server check in `doctor.get`; `profile.get` and
`daemon.status` are composed only from daemon-owned configuration and runtime
state.

Run the gate with:

```bash
go test -tags=integration ./internal/service/profile -run TestOpenIMProfileReadsIntegration
```

## OpenIM Conversation Integration

`internal/service/conversation` uses the same controlled deployment and three
shared secrets. The pre-provisioned test user must own at least one server
conversation. The gate verifies the fixed `/conversation/get_all_conversations`
and `/conversation/get_conversations` mappings through typed list/get/search,
including cursor handling when the account has multiple conversations.

Run the gate with:

```bash
go test -tags=integration ./internal/service/conversation -run TestOpenIMConversationReadsIntegration
```

OpenIM stores unread counts in the SDK's local database rather than exposing
them through these server endpoints. `conversation.unread` therefore remains
`not_validated` and must fail closed.

## OpenIM Message Integration

`internal/service/message` uses the authenticated `/msg/pull_msg_by_seq`
endpoint and never reads the SDK message database. Configure a controlled
conversation whose latest 100 server messages include the named message, a
text query match, and the message-window boundary. All IDs use the server
message ID, which is the ID emitted by the inbound bridge when available.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_CONVERSATION_ID`
- `ABDIM_OPENIM_MESSAGE_ID`
- `ABDIM_OPENIM_MESSAGE_QUERY`
- `ABDIM_OPENIM_AFTER_MESSAGE_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/service/message -run TestOpenIMMessageReadsIntegration
```

The test verifies history, local text search over the server-read window,
message lookup, cursor behavior, capability metadata, and grant-window
filtering. The server endpoint caps each read to the latest 100 messages, the
same bounded source window used by the typed service.

## OpenIM Social Integration

`internal/service/social` uses four authenticated server endpoints and never
reads the SDK friend or blacklist tables. Configure a controlled account with
the named friend and blacklist entries; `ABDIM_OPENIM_FRIEND_QUERY` must match
the named friend ID, nickname, or remark.

- `ABDIM_OPENIM_API_ADDR`
- `ABDIM_OPENIM_USER_ID`
- `ABDIM_OPENIM_TOKEN`
- `ABDIM_OPENIM_FRIEND_USER_ID`
- `ABDIM_OPENIM_FRIEND_QUERY`
- `ABDIM_OPENIM_BLACKLIST_USER_ID`

Run the gate with:

```bash
go test -tags=integration ./internal/service/social -run TestOpenIMSocialReadsIntegration
```

The test verifies friend list/get/search and blacklist list/get through the
typed service, including their scope-specific `available` capability metadata.
It does not create or modify data.
