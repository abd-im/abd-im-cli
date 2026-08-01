# ABD-049: Conversation Settings

状态：`ready`

## Outcome

允许 provider 在明确授权的单一会话上设置置顶和接收选项，不提供通用会话更新入口。

## Included Methods

- `conversation.set_pinned`
- `conversation.set_receive_option`

## Dependencies

`ABD-041` 的 conversation target isolation。

## Acceptance

- grant 只允许一个 conversation target。
- 输入限于 pinned boolean 或固定接收选项 enum；不公开通用 patch。
- 固定 server action 不读取 SDK local DB。
- 两个方法都有 operation/idempotency、未知结果 fail closed 和 integration gate。

## Development Record

尚未开始实现、验证或提交。
