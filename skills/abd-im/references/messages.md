# Messages

Discover a conversation ID before reading messages. All list limits are 1-100.

Read recent history:

```bash
printf '%s\n' '{"conversation_id":"conversation-id","limit":20}' | abdim --as user message history --params-stdin
```

Search one conversation:

```bash
printf '%s\n' '{"conversation_id":"conversation-id","query":"keyword","limit":20}' | abdim --as user message search --params-stdin
```

Read one known message:

```bash
printf '%s\n' '{"conversation_id":"conversation-id","message_id":"message-id"}' | abdim --as user message get --params-stdin
```

Send a deliberate new text message to exactly one target:

```bash
printf '%s\n' '{"recipient_id":"user-id","text":"Hello"}' | abdim --as user message send --params-stdin
printf '%s\n' '{"group_id":"group-id","text":"Hello team"}' | abdim --as user message send --params-stdin
```

Select `--as bot` instead when the requested sender is the Agent account. Never use this command to deliver the current inbound turn's final reply; the daemon does that automatically.
