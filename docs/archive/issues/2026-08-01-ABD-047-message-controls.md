# ABD-047: Message Controls

状态：`completed`（2026-08-01）

## Outcome

已交付 `message.send_location`、`message.send_custom` 和 `message.revoke`。位置、自定义 payload 和撤回均通过 typed manifest/grant、method-scoped targets 与 durable operation guard；撤回先由 server history 证明消息属于 profile owner，再调用固定 `/msg/revoke_msg`。

## Verification

- 单元、HTTP contract、provider registry 和全仓 `go test ./...` 通过。
- 受控 OpenIM integration 使用双账号通过 text、quote、at、location、custom 和 revoke；未知网络结果保持 `unknown`。
