# ABD-046: Attachment Foundation

状态：`ready`

## Outcome

为受限 provider 建立不透明附件引用和配额边界，使后续媒体与文件消息不接受任意本地路径或内容。

## Scope

- attachment metadata、profile/run 绑定、有效期和额度。
- 受控上传入口与类型、大小限制。

## Dependencies

`ABD-041` 的 method-scoped target allowlist。

## Acceptance

- provider 不能提交或取得任意本地路径。
- 每个 attachment reference 绑定 profile、run、额度和有效期。
- 正文、文件内容和完整路径不进入 control DB、审计或日志。
- fake 与 e2e 测试覆盖跨 profile/run、过期和配额拒绝。

## Development Record

尚未开始实现、验证或提交。
