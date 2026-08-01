# ABD-055: Compatibility Matrix

状态：`ready`

## Outcome

建立 SDK、OpenIM server 和 provider adapter 的兼容性矩阵及自动化 evidence gate。只有固定组合的 integration evidence 才能发布对应 capability 为 `available`；未验证组合保持 `not_validated` 或 `unsupported`。

## Dependencies

`ABD-048`、`ABD-052`、`ABD-053` 的 P3 capability surface，以及现有 capability manifest/status contract。

## Acceptance

- 矩阵记录 SDK、server、provider 版本和每项 capability 的验证结果、时间与失败原因。
- CI/release gate 能对支持组合执行固定 unit、contract、e2e 和 controlled integration 集合。
- capability response 和 provider construction snapshot 使用矩阵证据，不接受 manifest 静态声明越过 gate。
- 版本不匹配、证据过期或 gate 失败时 fail closed，且不泄露 token、正文或完整路径。

## Development Record

尚未开始实现或验证。
