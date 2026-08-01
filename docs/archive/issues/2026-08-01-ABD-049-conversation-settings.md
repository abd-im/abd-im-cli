# ABD-049: Conversation Settings

状态：`completed`（2026-08-01）

## Outcome

已交付 `conversation.set_pinned` 和 `conversation.set_receive_option`。输入限定为 pinned boolean 或固定 receive enum；source 先读取 server conversation identity，再调用只提交目标字段的 `/conversation/set_conversations`，不访问 SDK local DB。

## Verification

- 单元、HTTP contract、provider registry 和全仓 `go test ./...` 通过。
- 受控 OpenIM integration 使用双账号验证置顶、接收选项及恢复默认值。
