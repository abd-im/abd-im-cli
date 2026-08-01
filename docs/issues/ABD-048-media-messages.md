# ABD-048: Media Messages

状态：`ready`

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

## Development Record

尚未开始实现、验证或提交。
