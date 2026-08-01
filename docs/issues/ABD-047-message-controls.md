# ABD-047: Message Controls

状态：`ready`

## Outcome

交付不依赖附件的受限消息动作，允许 provider 在明确授权下发送位置、自定义消息和撤回自己的消息。

## Included Methods

- `message.send_location`
- `message.send_custom`
- `message.revoke`

## Dependencies

`ABD-041` 的 typed target isolation 和 `ABD-042` 的受限消息 operation 路径。

## Acceptance

- 三个方法都有独立 schema、manifest/grant allowlist、operation/idempotency 和固定 SDK/server integration gate。
- location 只接受有效经纬度和受限描述；custom payload 与 extension 有严格字节上限。
- revoke 只允许 profile owner 在 allowlisted conversation 中发送的消息。
- 所有未知结果保持 `unknown`，不以新 key 补发。

## Development Record

尚未开始实现、验证或提交。
