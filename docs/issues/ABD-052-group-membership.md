# ABD-052: Group Membership

状态：`ready`

## Outcome

交付受限的入群、退群、邀请成员和移除成员操作。

## Included Methods

- `group.join`
- `group.leave`
- `group.invite_members`
- `group.remove_members`

## Dependencies

`ABD-041` 的 group/user target isolation。

## Acceptance

- grant 同时约束 group 和涉及的 user；申请消息与成员数量有受限输入。
- leave、invite 和 remove 由固定 source/action 验证成员状态及操作者权限。
- 每个方法有 operation/idempotency、未知结果 fail closed 和 integration gate。

## Development Record

尚未开始实现、验证或提交。
