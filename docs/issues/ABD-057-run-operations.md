# ABD-057: Run Operations

状态：`ready`

## Outcome

交付 owner-only 的高级 run 运维能力族：查询 run 状态、显式取消指定 run，以及诊断关联 operation。运维接口只经 typed local service，不给 provider controller 权限，也不提供隐式重试。

## Dependencies

`ABD-032` 的 runtime/e2e lifecycle、`ABD-039` 的 cancellation/revocation 语义和现有 operation store。

## Acceptance

- owner 可分页查询 run 的 queued/running/completed/interrupted/cancelled 状态及有限诊断摘要，provider 不可调用这些方法。
- owner 可取消指定 run；取消会撤销 grant、关闭 proxy、终止 provider turn，并阻止后续 reply 或副作用。
- operation 诊断只返回 scope、目标摘要、状态、时间和脱敏错误；`unknown` 只能查询或显式标记，不自动重试。
- local RPC、权限、崩溃恢复、审计和隐私 e2e 通过，旧 P1 reply target 与授权边界保持不变。

## Development Record

尚未开始实现或验证。
