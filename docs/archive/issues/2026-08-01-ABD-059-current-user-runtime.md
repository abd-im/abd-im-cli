# ABD-059: Current-User Runtime

状态：`completed`

## Outcome

将 `abdim` 收敛为一个当前用户运行的本机 daemon：直接使用该用户已登录的 Codex CLI，不保留 root daemon、独立 provider UID 或 provider 配置模式。

## Dependencies

已交付的单 Codex adapter、run-private MCP boundary、隐私回归和 release automation。

## Acceptance

- `daemon serve` 不要求 root、`--provider-config` 或独立 OS 用户，并从当前用户 `PATH` 解析 `codex`。
- 每个 run 继续生成新的私有 `CODEX_HOME`、固定 MCP 配置和单连接 bridge；只复制当前用户 Codex 登录所需的认证材料，不继承其 MCP 配置。
- grant、typed proxy、撤销、event-bound reply 和隐私回归保持通过；不声称同 UID 具有 OS 级文件系统隔离。
- 移除 launcher、root provider e2e/CI gate 及所有当前文档中的旧部署路径；发布流程仅描述这一模式。

## Development Record

- 删除 `internal/launcher/` 和 root provider isolation e2e；`daemon serve` 改为解析当前用户 `codex`/`CODEX_HOME`。
- provider adapter 在每个 run 复制 `auth.json`，生成私有工作目录和固定 MCP 配置；源 Codex MCP 配置不继承。
- 架构、规格、连接器、测试、任务、CI 和发布文档统一为当前用户模式；release 脚本覆盖 Linux amd64/arm64 与 macOS amd64/arm64。
- 验证：`go test ./...`、`go vet ./...`、关键 `go test -race`、四目标 `go build`、`scripts/build-release.sh v0.1.0-rc1` 和 `git diff --check` 通过；`actionlint` 未安装，受控 OpenIM 环境变量未配置。
