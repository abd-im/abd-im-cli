# Testing

## Default

```bash
go test ./...
go vet ./...
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
