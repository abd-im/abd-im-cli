# ABD-051: Blacklist Management

状态：`completed`（2026-08-01）

## Outcome

已交付 `blacklist.add` 和 `blacklist.remove`。source 只调用固定 server relation endpoint；remove 前读取并验证当前 blacklist relationship，handler 绑定 profile owner、user target、operation/idempotency 和 unknown fail-closed。

## Verification

- 单元、HTTP contract、provider registry 和全仓 `go test ./...` 通过。
- 固定 server source 不使用 SDK local relation database；受控 OpenIM integration 使用双账号通过 add、state read、remove 生命周期并恢复状态。
