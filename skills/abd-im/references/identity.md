# Identities and discovery

The daemon maintains two independent local SDK sessions:

- `--as user`: the hosted owner account.
- `--as bot`: the Agent account and default identity.

The current turn prompt is authoritative. Hosted turns say that the Agent is replying on behalf of an owner and should use `--as user`; direct turns use `--as bot`. The daemon sends the final turn text through the correct SDK session.

Use these commands to inspect identity and discover stable IDs:

```bash
abdim --as user user me
printf '%s\n' '{"limit":20}' | abdim --as user conversation list --params-stdin
printf '%s\n' '{"query":"Alice","limit":20}' | abdim --as user conversation search --params-stdin
printf '%s\n' '{"limit":100}' | abdim --as user friend list --params-stdin
printf '%s\n' '{"query":"Alice","limit":20}' | abdim --as user friend search --params-stdin
```

Use `user get` only with IDs already obtained from ABD IM:

```bash
printf '%s\n' '{"user_ids":["user-id"]}' | abdim --as user user get --params-stdin
```

Available direct lookups:

```bash
printf '%s\n' '{"conversation_id":"conversation-id"}' | abdim --as user conversation get --params-stdin
printf '%s\n' '{"user_id":"user-id"}' | abdim --as user friend get --params-stdin
printf '%s\n' '{"limit":100}' | abdim --as user blacklist list --params-stdin
printf '%s\n' '{"user_id":"user-id"}' | abdim --as user blacklist get --params-stdin
```
