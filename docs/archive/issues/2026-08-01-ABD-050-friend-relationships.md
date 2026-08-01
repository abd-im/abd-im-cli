# ABD-050: Friend Relationships

状态：`completed`（2026-08-01）

## Outcome

已交付 `friend.request`、`friend.respond`、`friend.delete` 和 `friend.set_remark`。所有动作绑定 authenticated profile owner 和 allowlisted user；respond/delete 分别由固定 server source 验证 pending request 或 friend relationship，未知结果不补发。

## Verification

- 单元、HTTP contract、provider registry 和全仓 `go test ./...` 通过。
- 受控 OpenIM integration 使用双账号完成申请、接受、备注、删除完整生命周期；fixture 会先清理既有状态。
