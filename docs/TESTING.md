# Testing

## Default

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

默认测试不需要真实 Codex 或 OpenIM 凭据，覆盖：

- 双账号 setup、token-free profile 和隔离的 SDK 路径；
- 一个进程锁下的 user/bot SDK 生命周期；
- CLI 默认 bot、`--as user` 和本地 RPC 身份路由；
- direct/hosted prompt、上下文读取、去重和自动回复身份；
- conversation 串行、有界并发、deadline、取消和 provider session resume；
- OpenIM callback 规范化和文本发送；
- Codex workdir 中的静态 `abd-im` Skill。

常用聚焦命令：

```bash
go test ./cmd/abdim ./internal/daemon ./internal/bridge/abdim
go test ./internal/agent/...
go test ./skills
go test -race ./internal/agent/... ./internal/daemon/...
```

## OpenIM Integration

带 `integration` build tag 的读取测试需要 disposable 账号的短期 token：

```bash
go test -tags=integration ./internal/service/profile -run TestOpenIMProfileReadsIntegration
go test -tags=integration ./internal/service/conversation -run TestOpenIMConversationReadsIntegration
go test -tags=integration ./internal/service/group -run TestOpenIMGroupReadsIntegration
go test -tags=integration ./internal/service/social -run TestOpenIMSocialReadsIntegration
```

具体环境变量以测试中的 `ABDIM_OPENIM_*` 检查为准。默认 CI 不接触真实账号。

## Server Boundary

`openim-chat` 的托管通知与协议改动在其仓库验证：

```bash
go test ./internal/rpc/bot ./internal/api/bot ./pkg/protocol/bot
```

该测试确认 business notification 只携带稳定引用且没有服务端加载的历史数组。
