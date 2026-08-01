# ABD-050: Friend Relationships

状态：`ready`

## Outcome

交付受限的好友申请、响应、删除和备注设置，覆盖完整的好友关系生命周期。

## Included Methods

- `friend.request`
- `friend.respond`
- `friend.delete`
- `friend.set_remark`

## Dependencies

`ABD-041` 的 user target isolation。

## Acceptance

- 每个动作绑定 profile owner 和 allowlisted user，provider 不能指定发起者。
- request message、response enum 和 remark 都受严格输入限制。
- respond 与 delete 由固定 source 验证当前申请或好友关系。
- 每个方法有 operation/idempotency、未知结果 fail closed 和 integration gate。

## Development Record

尚未开始实现、验证或提交。
