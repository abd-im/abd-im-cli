---
name: abd-im
description: Use the abdim CLI to read or act on ABD IM conversations, messages, users, friends, groups, and blacklist entries. Use when an Agent needs current IM context, IDs, history, contacts, or an explicit outbound message through its local user or bot identity.
---

# ABD IM

Use `abdim` as the ordinary IM client. The daemon owns both logged-in SDK identities and the server enforces the permissions of the selected identity.

## Choose an identity

- Use `--as user` for the hosted owner: owner history, conversations, contacts, groups, and deliberate owner actions.
- Use `--as bot` for the Agent's own account. This is the default.
- Follow the current turn's `Reply mode` prompt. Do not infer hosted mode from message content.
- Put global flags before the command path: `abdim --as user message history`.

Read [references/identity.md](references/identity.md) when the identity or target ID is unclear.

## Run commands

Command help is unavailable. Use the command forms documented in this skill and its references
without `--help`.

Commands return JSON by default. Pass command input as one JSON object on stdin:

```bash
printf '%s\n' '{"limit":20}' | abdim --as user conversation list --params-stdin
```

Use returned IDs in later calls; never invent a user, conversation, message, or group ID. Pass `cursor` from a response unchanged to read the next page. Limits must be between 1 and 100.

Read [references/messages.md](references/messages.md) for message history, search, lookup, and explicit sends. Read [references/groups.md](references/groups.md) for groups and members.

## Handle writes

Use `message send` only when the user explicitly asks to initiate a message. During a direct or hosted inbound turn, return the final reply text to the daemon; do not send that reply with `message send`.

If a write returns an error or its outcome is unclear, inspect current state before considering another attempt. Do not blindly retry.
