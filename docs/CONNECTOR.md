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
ID, or pairing step. Every supported inbound message not sent by the bot itself
can create a run. Each run receives the full set of methods currently marked
`available`, while every provider call still passes through the run-private MCP
bridge, typed proxy, grant, operation guard, and event-bound reply path. Replies
remain fixed to the conversation that triggered them.

Without an inbound identity rule, any account that can message the bot can
trigger its available capabilities. This is the explicit security tradeoff of
the zero-configuration inbound model.

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

Local management CLI commands and `abdim mcp serve` connect to the
already-running daemon. They never initialize the SDK or open its data
directory themselves. In architecture and method names, "owner" means this
current local OS user; it is not a configured IM identity.

## MCP

IM-triggered Codex runs need no MCP configuration. The daemon creates a fresh
run-private MCP server automatically and exposes the currently available read
and action tools through that run's grant.

A trusted local Codex client can separately register the management MCP:

```bash
codex mcp add abdim-work -- \
  /absolute/path/to/abdim --profile work mcp serve
```

This local management MCP exposes typed reads, diagnostics, run cancellation,
and operation diagnostics. It does not expose provider action tools; message,
group, friend, blacklist, and conversation actions are available inside the
automatically constructed IM-triggered MCP run.

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
