# ABD-053: Group Administration

状态：`ready`

## Outcome

交付受限群资料、全员禁言、成员禁言和群主转让操作。

## Included Methods

- `group.set_info`
- `group.set_mute`
- `group.set_member_mute`
- `group.transfer_owner`

## Dependencies

`ABD-041` 的 group/user target isolation；`ABD-052` 已确认群成员动作的固定 action source。

## Acceptance

- grant 同时约束 group 和成员 target。
- 只公开有限群资料字段、mute boolean 与有上限的成员禁言时长，不暴露通用 patch。
- 固定 source/action 验证管理或 owner 权限。
- 每个方法有 operation/idempotency、未知结果 fail closed 和 integration gate。

## Development Record

尚未开始实现、验证或提交。
