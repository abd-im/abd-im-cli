# Testing

## Default

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

默认测试不需要真实 Codex 或 OpenIM 凭据，覆盖：

- setup、daemon 生命周期和私有 token 文件。
- conversation 队列、run 持久化、取消和重启中断。
- run-private `abdim` CLI 与 method grant。
- 消息窗口、event-bound reply 和写操作幂等。
- fake Codex app-server、fake ACP Agent 和 Stream reply。

常用聚焦命令：

```bash
go test ./internal/agent/...
go test ./internal/daemon/... ./tests/e2e/...
go test -race ./internal/agent/... ./internal/daemon/... ./internal/reply/... ./tests/e2e/...
```

## Real Codex

真实 Codex 测试会构建当前 `abdim`，启动本机 `codex app-server`，让 Codex 调用
`abdim commands` 和一个授权的 `message.history` 方法。需要已登录的 Codex CLI
和网络：

```bash
ABDIM_REAL_CODEX=1 go test -tags=integration \
  ./internal/agent/provider/codex -run TestRealCodexAppServer -count=1 -v
```

只编译 integration test 而不发起真实请求：

```bash
go test -tags=integration ./internal/agent/provider/codex -run '^$'
```

## OpenIM Integration

OpenIM integration tests 使用 `integration` build tag，位于对应 service、capability
和 bridge package。它们需要短期测试 token 和 disposable 账号；部分测试会创建群、
发送消息或修改关系。具体变量以测试中的 `ABDIM_OPENIM_*` 检查为准。

示例：

```bash
go test -tags=integration ./internal/service/profile -run TestOpenIMProfileReadsIntegration
go test -tags=integration ./internal/service/message -run TestOpenIMMessageReadsIntegration
go test -tags=integration ./internal/capability/message -run 'TestOpenIM(TextSend|QuoteSend|TextAt)Integration'
```

`.github/workflows/controlled-integration.yml` 是完整的受控 OpenIM 验证入口。
默认 CI 只运行无凭据测试、vet 和 release build，不接触生产账号。
