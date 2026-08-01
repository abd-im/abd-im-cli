# ABD-048: Media Messages

状态：`completed`（2026-08-01）

## Outcome

通过 ABD-046 的附件引用，交付受限图片、文件、语音和视频消息发送。

## Included Methods

- `message.send_image`
- `message.send_file`
- `message.send_sound`
- `message.send_video`

## Dependencies

`ABD-046` 的 attachment metadata、quota 和不透明引用。

## Acceptance

- 每个方法只接受同 profile、同 run、未过期且类型匹配的 attachment reference。
- 文件名、大小、媒体时长和目标都受 grant 限制；视频同时校验缩略图引用。
- provider、日志和 control DB 不获得本地路径或内容。
- 上传或发送的未知结果不补发，且每个方法通过固定 SDK/server integration gate。

## Verification

- attachment reference、类型、target、文件名、时长、idempotency 和 unknown outcome 均由 unit/proxy 测试覆盖；provider MCP 只公开 snapshot 允许的 typed tools。
- 受控 OpenIM SDK/server gate 使用两账号通过图片、文件、语音和视频上传回调；token、文件内容和本地路径未写入仓库、control DB 或 provider 输入。
