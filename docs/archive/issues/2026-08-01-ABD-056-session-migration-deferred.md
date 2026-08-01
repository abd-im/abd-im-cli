# ABD-056: Session Migration

状态：`deferred`（2026-08-01）

## Decision

session migration 依赖多 provider/adapter 兼容边界，随 `ABD-054` 一并暂缓。当前 run 继续使用单 Codex provider 的独立 session；中断 run 不自动 replay。

## Reactivation

多 provider 范围重新启用后，再基于实际 provider session contract 建立 versioned envelope 和迁移验收。
