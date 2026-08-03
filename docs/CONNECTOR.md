# Setup and Local Runtime

`abdim` supports one local deployment model: the current user runs the daemon,
the daemon logs in to the fixed ABD OpenIM deployment, and provider runs reuse
that user's existing Codex login. Do not use `sudo`.

## First Setup

Install and log in to the Codex CLI first, then run:

```bash
abdim setup
```

For a named bot profile, place the global option before the command:

```bash
abdim --profile work setup
```

`setup` prompts for the ABD bot account and password, exchanges them for the
canonical OpenIM user ID and short-lived IM token, writes the owner-only local
profile, and starts the daemon in the background. Phone accounts default to
area code `+86`; email accounts do not prompt for an area code.

The password is read without terminal echo and is never persisted. The token is
stored in an owner-only `0600` file and is never placed in argv, profile TOML,
logs, audit records, MCP payloads, or RPC responses. The current ABD endpoints
and platform are built in:

```text
Account login = https://2.alissa.xin/chat/account/login
OpenIM API    = https://2.alissa.xin/api
OpenIM WS     = wss://2.alissa.xin/msg_gateway
Platform      = 7
```

`setup` prints a one-time pairing message such as `pair A1B2C3D4`. Within 15
minutes, send that exact text to the bot in a private conversation from the ABD
account that will own it. The daemon stores only a SHA-256 digest while pairing
is pending. Before pairing succeeds, inbound messages are consumed without
creating a Codex run. A successful pairing persists the canonical sender ID;
later setup runs preserve that owner binding. Run `abdim setup` again if the
code expires before pairing.

The paired owner is the only inbound principal. Its accepted messages receive
the full set of methods currently marked `available`, while every provider call
still passes through the run-private MCP bridge, typed proxy, grant, operation
guard, and event-bound reply path. Other contacts cannot trigger a run. Replies
remain fixed to the conversation that triggered them.

## Lifecycle

Setup starts the daemon automatically. Normal operation uses only:

```bash
abdim status
abdim stop
abdim start
abdim restart
```

Use `--profile work` before the command for a non-default profile. The daemon is
a detached child of the installed `abdim` binary and runs as the current user.
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

Owner CLI commands and `abdim mcp serve` connect to the already-running local
daemon. They never initialize the SDK or open its data directory themselves.

## Capability Gate

A typed source is marked `available` only after fixed SDK/server integration
evidence exists. The daemon does not read the SDK chat database. Profile,
conversation, message, social, group, and action sources use authenticated
server APIs through the daemon-owned SDK context; unsupported mappings remain
`not_validated` and are omitted from provider discovery.

Remote side effects use method-scoped targets and durable operation identities.
Network or undecodable response failures remain `unknown`; neither the daemon
nor provider retries them with a new idempotency key. `conversation.unread`
remains `not_validated` because the current server API does not expose it.
