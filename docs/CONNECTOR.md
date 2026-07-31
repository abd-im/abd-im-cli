# Deployment Connector

`abdim-cli` does not own account login or server deployment. A deployment
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

After importing the token, configure the non-secret deployment fields for a
Linux daemon with the canonical user ID returned by that login flow:

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

## Isolated Codex Daemon

The only configured provider adapter is the local `codex` CLI in App Server
stdio mode. It is not selected by an arbitrary command or endpoint. Release
deployment runs the daemon as root and Codex as a separately provisioned,
non-root OS UID/GID. The provider account owns its home and Codex home; the
daemon owns the run root and its profile, SDK, credentials, and owner socket.

`daemon serve` requires an absolute `--provider-config` path to a root-owned,
non-group/other-writable regular file beneath a root-owned traversable
directory. Its exact fields are `uid`, `gid`,
`home`, `codex_home`, `codex_path`, and `run_root`: the first two are positive,
non-root numeric IDs; `home` must be directly under a root-owned traversable
directory; `codex_home` must be inside `home`; `home` and `codex_home` must be
owned by that UID/GID and not group/other writable;
`codex_path` must be an absolute root-owned, provider-executable path beneath
a root-owned traversable directory and name a
non-group/other-writable regular file; `run_root` must be root-owned,
non-writable by group/other, and permit provider traversal. The provider's
`auth.json` must be a regular file owned by the configured UID/GID.

Before starting the daemon, provision the provider user, create its owner-only
home and Codex home, authenticate Codex as that user, create the root-owned
run root with mode `0711`, and install the provider configuration under the
deployment's root-controlled configuration directory. The run root must not
be inside a provider-writable path.

Start the isolated daemon with the credential and inbound-policy
acknowledgements plus the controlled provider configuration:

```bash
sudo abdim --profile work daemon serve \
  --allow-plaintext-credentials \
  --allow-all-inbound \
  --provider-config /etc/abdim/providers/work.toml
```

`daemon serve` writes one JSON ready response, then remains in the foreground.
It starts the SDK, opens the owner Unix socket only after login succeeds, and
passes each accepted inbound text message to the fixed Codex App Server. The
daemon persists only event identity and reply target; message text is transient
provider input and is not stored in `control.db` or emitted in CLI responses.
The reply service uses the callback-derived private recipient or group ID, so a
provider prompt cannot choose another destination.

`--allow-all-inbound` currently enables private and group messages from other
users. It is deliberately not the default group policy and remains an explicit
operator acknowledgement.

Each Codex run now receives a fresh private `CODEX_HOME` containing only a
fixed `abdim` stdio MCP server configuration. That server's subprocess only
bridges to one run-private Unix socket; the daemon retains the grant and typed
tool proxy. Its tool list is fixed before Codex starts to the policy, verified
capability, and grant intersection. The adapter declines Codex command/file
approvals. The run's Codex home, working directory, and socket are assigned to
the provider UID only after the daemon writes their fixed contents; the
root-owned run parent prevents the provider from preparing paths for later
runs. Unverified service sources remain `not_validated` and are absent from
provider MCP discovery.

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
4. Construct the fixed Codex provider, inbound policy/reply/run dependencies,
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
