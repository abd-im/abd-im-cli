# Groups

List or search groups before using a group ID:

```bash
printf '%s\n' '{"limit":20}' | abdim --as user group list --params-stdin
printf '%s\n' '{"query":"project","limit":20}' | abdim --as user group search --params-stdin
printf '%s\n' '{"group_id":"group-id"}' | abdim --as user group get --params-stdin
```

List or search members of one known group:

```bash
printf '%s\n' '{"group_id":"group-id","limit":100}' | abdim --as user group members list --params-stdin
printf '%s\n' '{"group_id":"group-id","query":"Alice","limit":20}' | abdim --as user group members search --params-stdin
```

Use the response cursor unchanged in another request when more results are available.
