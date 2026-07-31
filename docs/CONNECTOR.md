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

## Local Codex Daemon

The only currently configured provider adapter is the local `codex` CLI in
App Server stdio mode. It is not selected by an arbitrary command or endpoint.
First verify that `codex app-server --listen stdio://` works with a separately
managed Codex home directory. That directory contains provider credentials and
must not be an abdim profile, SDK, runtime, or configuration directory.

For a local development trial, start the daemon with all three explicit
acknowledgements:

```bash
go run ./cmd/abdim --profile work daemon serve \
  --allow-plaintext-credentials \
  --allow-all-inbound \
  --allow-unsafe-same-user-provider \
  --codex-home "$CODEX_HOME"
```

`daemon serve` writes one JSON ready response, then remains in the foreground.
It starts the SDK, opens the owner Unix socket only after login succeeds, and
passes each accepted inbound text message to the fixed Codex App Server. The
daemon persists only event identity and reply target; message text is transient
provider input and is not stored in `control.db` or emitted in CLI responses.
The reply service uses the callback-derived private recipient or group ID, so a
provider prompt cannot choose another destination.

`--allow-all-inbound` currently enables private and group messages from other
users. It is deliberately not the default group policy. `--allow-unsafe-same-user-provider`
is required because setting `HOME`, `CODEX_HOME`, and a private working
directory does not isolate a process running under the daemon owner's OS UID.
Do not use this mode for a release or untrusted inbound traffic. The release
path still requires an independent user or container launcher that exposes only
the run-private tool proxy and the provider's own home.

This first adapter provides text replies only. It declines Codex command/file
approvals and does not yet attach the provider MCP/tool loop. The daemon still
registers typed services for owner diagnostics, but unverified service sources
remain `not_validated` and are unavailable to both owner and provider.

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

The group HTTP source currently uses the daemon-private SDK context only for
authenticated server requests. Its controlled integration test requires
`ABDIM_OPENIM_API_ADDR`, `ABDIM_OPENIM_USER_ID`, and `ABDIM_OPENIM_TOKEN`.
