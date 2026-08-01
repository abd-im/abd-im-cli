# ABD-046: Attachment Foundation

状态：`completed`，2026-08-01

## Outcome

为受限 provider 建立不透明附件引用和配额边界，使后续媒体与文件消息不接受任意本地路径或内容。

## Scope

- attachment metadata、profile/run/grant 绑定、有效期和额度。
- 受控上传入口与类型、大小限制。

## Dependencies

`ABD-041` 的 method-scoped target allowlist。

## Acceptance

- provider 不能提交或取得任意本地路径。
- 每个 attachment reference 绑定 profile、run、额度和有效期。
- 正文、文件内容和完整路径不进入 control DB、审计或日志。
- fake 与 e2e 测试覆盖跨 profile/run、过期和配额拒绝。

## Development Record

- 实现：profile 私有目录只按 opaque reference 解析文件；control DB 只记录 reference、profile/run/grant、类型、字节数、byte limit 和到期时间。grant 和 daemon policy 都透传 attachment byte limit；同一 grant 的 quota reservation 在单一 DB transaction 中执行。
- 验证：新增 profile、grant、control、message unit tests 和 attachment boundary e2e，覆盖路径拒绝、跨 profile/run/grant 拒绝、过期、类型、配额和控制库 schema。
- 完成提交：`feat: add run-scoped attachment foundation`。
