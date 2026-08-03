# Setup and Local Runtime

`abdim` supports one local deployment model: the current user runs the daemon,
the daemon logs in to the fixed ABD OpenIM deployment, and provider runs reuse
that user's existing Codex login. Do not use `sudo`.

## First Setup

Install and log in to the Codex CLI first. From the repository root, build the
binary into the current directory and run setup:

```bash
go build -o ./abdim ./cmd/abdim
./abdim setup
```

For a named bot profile, place the global option before the command:

```bash
./abdim --profile work setup
```

`setup` prompts for the ABD bot account and password, exchanges them for the
canonical OpenIM user ID and short-lived IM token, writes the local profile,
and starts the daemon in the background. Phone accounts default to area code
`+86`; email accounts do not prompt for an area code.

The password is read without terminal echo and is never persisted. The token is
stored in a current-user-only `0600` file and is never placed in argv, profile
TOML, logs, audit records, MCP payloads, or RPC responses. The current ABD
endpoints and platform are built in:

```text
Account login = https://2.alissa.xin/chat/account/login
OpenIM API    = https://2.alissa.xin/api
OpenIM WS     = wss://2.alissa.xin/msg_gateway
Platform      = 7
```

Setup is complete when the command returns; there is no second account, owner
ID, or pairing step. A direct message not sent by the bot itself can create a
run. Inbound tools are disabled by default, so the initial provider MCP tool
list is empty and the only externally visible effect is the event-bound reply
to the trigger conversation. Group messages are ignored.

Without an inbound identity rule, any account that can directly message the bot
can consume provider capacity and receive generated text. In the default mode
it cannot query IM state or invoke typed actions.

To expose all capability-verified IM tools to direct-message runs, explicitly
enable them for the profile:

```bash
./abdim inbound tools enable
./abdim inbound tools status
```

This is a profile-wide high-trust switch: every account that can directly
message the bot receives the verified typed tool set, including write actions,
without a per-call confirmation prompt. Target checks, operation idempotency,
rate and attachment budgets, and the run-private grant remain enforced. Message
history and quote sources remain limited to messages before the trigger in that
same direct conversation. Do not enable this mode on a publicly reachable bot.
Disable it with `./abdim inbound tools disable`.

## Lifecycle

Setup starts the daemon automatically. Normal operation uses only:

```bash
./abdim status
./abdim stop
./abdim start
./abdim restart
./abdim inbound tools status
```

Use `--profile work` before the command for a non-default profile. The daemon is
a detached child of the invoked `abdim` binary and runs as the current user.
It resolves `codex` from that user's `PATH`, reads the existing login from
`$CODEX_HOME` (default `~/.codex`), and writes diagnostics to the profile's
private `logs/daemon.log`. No root account, system service, external provider
configuration, or foreground daemon command is supported.

Each Codex run receives a fresh private `CODEX_HOME`. It copies only login and
non-MCP model/provider configuration, removes source MCP and history data, and
installs one fixed run-private `abdim` MCP bridge. The adapter rejects command
and file approvals and removes the run directory on close. This is a trusted
current-user model, not an operating-system sandbox: a malicious process with
the same UID may access other files owned by that user.

Local management CLI commands and `./abdim mcp serve` connect to the
already-running daemon. They never initialize the SDK or open its data
directory themselves. In architecture and method names, "owner" means this
current local OS user; it is not a configured IM identity.

## MCP

IM-triggered Codex runs need no manual MCP configuration. The daemon creates a
fresh run-private MCP server automatically. Its `enabled_tools` list is empty
by default and contains every capability-verified typed IM method after
`./abdim inbound tools enable`.

A trusted local Codex client can separately register the management MCP:

```bash
codex mcp add abdim-work -- \
  /absolute/path/to/abdim --profile work mcp serve
```

This local management MCP exposes typed reads, diagnostics, run cancellation,
and operation diagnostics. It does not expose provider action tools; those are
available only inside an enabled, run-private inbound MCP grant.

## Capability Gate

A typed source is marked `available` only after the complete controlled
SDK/server integration workflow passes for the fixed compatibility tuple. The
daemon does not read the SDK chat database. Profile, conversation, message,
social, group, and action sources use authenticated server APIs through the
daemon-owned SDK context; unsupported mappings remain `not_validated`.
Capability status records implementation evidence, not admission: inbound
discovery remains empty while tools are disabled and includes only `available`
methods while tools are enabled.

Remote side effects use method-scoped targets and durable operation identities.
Network or undecodable response failures remain `unknown`; neither the daemon
nor provider retries them with a new idempotency key. `conversation.unread`
remains `not_validated` because the current server API does not expose it.
