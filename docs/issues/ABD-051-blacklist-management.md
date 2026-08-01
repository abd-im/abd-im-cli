# ABD-051: Blacklist Management

状态：`ready`

## Outcome

交付受限的黑名单添加和移除，保持关系状态由固定 server source 验证。

## Included Methods

- `blacklist.add`
- `blacklist.remove`

## Dependencies

`ABD-041` 的 user target isolation。

## Acceptance

- 每个动作只允许一个 allowlisted user，且绑定 profile owner。
- remove 之前由固定 source 验证该 user 当前在 profile owner 的黑名单中。
- 两个方法都有 operation/idempotency、未知结果 fail closed 和 integration gate。

## Development Record

尚未开始实现、验证或提交。
