# Local Runtime Connector

`abdim-cli` does not own account login or server deployment. The local runtime
connector constructs the daemon-owned `internal/bridge/abdim.Config` and
injects it into `internal/daemon.Runtime`. The reusable validation and
credential-resolution boundary is `internal/connector.Prepare`.

The connector supplies:

- `ProfileID` and authenticated `UserID`;
- the token as an in-memory `[]byte` resolved from the configured credential
  store;
- SDK `IMConfig.ApiAddr`, `IMConfig.WsAddr`, `IMConfig.PlatformID`, per-profile
  `DataDir`, and per-profile `LogFilePath`.

For the current ABD deployment, the non-secret SDK endpoints are:

```text
ApiAddr = https://2.alissa.xin/api
WsAddr  = wss://2.alissa.xin/msg_gateway
```

The login account and password are not connector configuration. The external
login flow must return an `imToken` and the canonical `userID`; import that
token through the local credential store and pass only its opaque reference to
the daemon.

After importing the token, configure the non-secret deployment fields with the
canonical user ID returned by that login flow:

```bash
go run ./cmd/abdim --profile work profile configure \
  --user-id "$ABDIM_USER_ID" \
  --api-addr https://2.alissa.xin/api \
  --ws-addr wss://2.alissa.xin/msg_gateway \
  --platform-id 7
```

`profile configure` stores these fields in the owner-only profile file. Its
response intentionally omits the user ID, endpoints, and credential reference.

Verify the imported credential and SDK connection without starting a provider
or exposing the owner socket:

```bash
go run ./cmd/abdim --profile work daemon verify --allow-plaintext-credentials
```

The command starts the daemon-owned SDK lifecycle, logs in, logs out, and
returns only whether the profile was verified.

## Current-User Codex Daemon

The only configured provider adapter is the local `codex` CLI in App Server
stdio mode. Run `abdim` as the same local user that runs `codex`; the daemon
resolves `codex` from that user's `PATH` and reads its login from
`$CODEX_HOME/auth.json` (default `~/.codex/auth.json`). Do not start the daemon
through `sudo`, because that would select root's profile and Codex login.

Start the normal inbound daemon with the credential and inbound-policy
acknowledgements:

```bash
abdim --profile work daemon serve \
  --allow-plaintext-credentials \
  --allow-all-inbound
```

`daemon serve` writes one JSON ready response, then remains in the foreground.
It starts the SDK, opens the owner Unix socket only after login succeeds, and
passes each accepted inbound text message to the fixed Codex App Server. The
daemon persists only event identity and reply target; message text is transient
provider input and is not stored in `control.db` or emitted in CLI responses.
The reply service uses the callback-derived private recipient or group ID, so a
provider prompt cannot choose another destination.

`--allow-all-inbound` currently enables private and group messages from other
users, but each resulting provider run receives only `message.history` for the
triggering conversation and its bounded message window. It is deliberately not
the default group policy and remains an explicit operator acknowledgement.

For a controlled capability test, start an owner-only full-access run instead:

```bash
abdim --profile work daemon serve \
  --allow-plaintext-credentials \
  --inbound-policy owner-full \
  --owner-user-id "$ABDIM_TEST_OWNER_USER_ID"
```

`owner-full` accepts only inbound messages whose canonical OpenIM `sender_id`
matches `--owner-user-id`; it cannot be combined with `--allow-all-inbound`.
Those trusted test runs receive every currently `available` typed provider
method, unrestricted typed targets and message reads, a 64-call budget, and a
32 MiB attachment budget. Their listed MCP tools are pre-approved for the
run, so the non-interactive Codex App Server does not wait for an approval
reply that the daemon cannot provide. They still expire after two minutes, use
the same operation/idempotency guards, and can reply only to the
callback-derived conversation. This mode is for a controlled test account, not
a deployment policy for ordinary contacts. It does not provide scheduled or
background message delivery.

Each Codex run receives a fresh private `CODEX_HOME` containing the copied
current-user login and the current user's non-MCP model/provider configuration
(including a configured provider `base_url`). Source MCP and history tables are
removed, then replaced with one fixed `abdim` stdio MCP server and disabled
history persistence. That server's subprocess only bridges to one run-private
Unix socket; the daemon retains the grant and typed tool proxy. Its tool list is
fixed before Codex starts to the policy, verified capability, and grant
intersection. The adapter declines Codex command/file approvals. Unverified
service sources remain `not_validated` and are absent from provider MCP
discovery. The run-private provider bridge negotiates the MCP initialization
version offered by the fixed Codex app-server; owner MCP retains its fixed
local-service protocol contract.

This is intentionally a simple trusted-user deployment, not an operating
system sandbox. A same-user Codex process may still access files readable by
that user, including the daemon profile and owner socket. The security boundary
for normal provider calls is the run-private MCP bridge, typed proxy, grant,
and event-bound reply path; do not use this mode for hostile local code.

The token must not be placed in argv, profile TOML, environment dumps, logs,
MCP payloads, or RPC responses. `profile.toml` stores only its opaque
`credential_ref`; the existing file fallback is disabled unless the owner
explicitly enables it.

The connector composition order is:

1. Build profile paths with `internal/profile.NewPaths` and acquire the profile
   lock through `daemon.Runtime`.
2. Call `connector.Prepare` with the deployment settings and credential store;
   it returns an `abdim.Adapter` without initializing the SDK.
3. Construct all verified typed service sources and `daemon.OwnerMethods`.
4. Resolve the current user's Codex executable and login, then construct the
   fixed Codex provider, inbound policy/reply/run dependencies,
   and `daemon.Runtime`.
5. Start `Runtime`; it initializes the SDK and only then serves the owner Unix
   socket.

The current CLI owner commands and `abdim mcp serve` do not start this
connector. They connect to the socket of an already-running daemon. This is
intentional: CLI and MCP must never initialize the SDK or open its data
directory.

## Capability Gate

Every service source must have a fixed SDK/server integration test before its
capability is marked `available`. A server endpoint that has different input
semantics from the typed service remains `not_validated` until a dedicated
mapping and privacy test exists. In particular, do not implement message
history/search by calling SDK methods that read its local SQLite database.

The profile source combines daemon-owned profile/runtime facts with the fixed
authenticated `/user/get_users_info` endpoint. `user.me`, `user.get`, and the
server check in `doctor.get` use that endpoint; `profile.get` and
`daemon.status` do not access the SDK database. Its controlled integration
gate uses `ABDIM_OPENIM_API_ADDR`, `ABDIM_OPENIM_USER_ID`, and
`ABDIM_OPENIM_TOKEN`.

The group and conversation HTTP sources use the daemon-private SDK context
only for authenticated server requests. Their controlled integration tests
require `ABDIM_OPENIM_API_ADDR`, `ABDIM_OPENIM_USER_ID`, and
`ABDIM_OPENIM_TOKEN`. The conversation source verifies
`/conversation/get_all_conversations` and `/conversation/get_conversations`
for `list`, `get`, and `search`; OpenIM unread counts remain local SDK state,
so `conversation.unread` stays `not_validated`.

The message source uses the fixed authenticated `/msg/pull_msg_by_seq`
endpoint, which verifies the token user and conversation membership before
returning messages. It never uses `/msg/search_msg`: that endpoint lacks a
text-query field and is not a suitable user-scoped message read. The typed
message service reads at most the latest 100 server messages, filters text
search locally within that bounded result, and applies its cursor and grant
message window before returning data.

The social source uses `/friend/get_friend_list`,
`/friend/get_designated_friends`, `/friend/get_black_list`, and
`/friend/get_specified_blacks`. Each endpoint verifies the token user against
the requested `userID` or `ownerUserID`; lists are read page by page from the
server and `friend.search` filters only that authenticated friend result.
Neither friend nor blacklist reads call SDK local-table APIs.

`group.create` calls the authenticated `/group/create_group` server action
directly through the daemon-owned SDK context. It fixes `OwnerUserID` to the
authenticated daemon user, fixes the group type to `WorkingGroup`, and copies
only the handler input's allowlisted member IDs. It does not call the SDK
`Group.CreateGroup` convenience API because that API synchronizes local SDK
state after a successful request. Network, HTTP-status, or undecodable-response
failures remain `unknown` operations, so a later idempotency key cannot create
another group with the same input. The default inbound policy does not grant
this write method.
