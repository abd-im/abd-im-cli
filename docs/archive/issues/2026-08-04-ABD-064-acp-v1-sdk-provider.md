# ABD-064: ACP v1 SDK Provider

状态：completed

## 范围

- 使用 `github.com/coder/acp-go-sdk` 实现 ACP v1 stdio provider。
- 支持 initialize、session/new、session/prompt、session/update、session/cancel、session/close。
- 聚合 Agent message text chunk，并把规范化完整文本交给 output sink。
- 默认拒绝 permission，关闭时回收进程组和 run-private MCP bridge。
- JSON-RPC framing、request ID、ACP wire model 和 connection lifecycle 由 SDK 持有。

## 非目标

- 不在 Server 或 SDK 中引入 ACP。
- 不在本 task 完成 Codex、Hermes、OpenClaw 的完整真实兼容矩阵。

## 验证

- fake ACP v1 Agent 验证 initialize、MCP 注入、message chunk、permission、cancel 和 close。
- 非 v1 协商明确返回 `PROTOCOL_UNSUPPORTED`。
- 只有无效 stdout、没有 initialize 响应或进程提前退出时 fail-closed。
- `go test -race ./internal/agent/provider/acp ./internal/reply ./internal/daemon ./internal/bridge/abdim`。

## 结果

- 生产装配已切换到 ACP v1 provider，profile 只保存 `codex`、`hermes` 或
  `openclaw` ID。
- ACP 仅位于 CLI；SDK 与 Server 使用 provider-agnostic Stream contract。
- 手写 ACP JSON-RPC 和版本 compatibility surface 已删除。
- 完成 commit：待提交。
