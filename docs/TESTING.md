# Testing

## Default

```bash
go test ./...
go vet ./...
```

## P1 Runtime E2E

`tests/e2e/runtime_inbound_reply_test.go` is a no-credential, in-process P1
gate and is included in `go test ./...`. It composes the daemon runtime with
the shared fake SDK/provider, verifies the profile lock before SDK allocation,
then verifies the ready Unix socket, callback deduplication, one reply slot and
event-bound reply. It reopens the control database and verifies restart work is
recorded as `state.reconciled`, not as a fabricated inbound message.

Run it directly with:

```bash
go test ./tests/e2e -run TestRuntimeInboundReplyE2E
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
