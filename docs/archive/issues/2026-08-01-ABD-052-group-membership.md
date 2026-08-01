# ABD-052: Group Membership

状态：`completed`（2026-08-01）

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

## Verification

- unit/proxy 和 HTTP contract 测试覆盖 manifest、group/user grant、角色与成员状态拒绝、operation/idempotency 和 unknown 终态；daemon/provider registry 只映射四个固定 typed methods。
- 两账号受控 server gate 创建临时群后完成 leave、invite、remove 与 join；实现只调用固定 server source/action，不调用 SDK Group API。
