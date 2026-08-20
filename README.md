# abd-im-cli

[![CI](https://github.com/abd-im/abd-im-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/abd-im/abd-im-cli/actions/workflows/ci.yml)

`abdim` 是本机 ABD IM daemon 和普通 CLI。一个 profile 同时登录两个独立的 OpenIM SDK
身份：`bot` 接收发给 Agent 的消息和托管通知，`user` 代表 owner 读取会话上下文并投递托管回复。

当前稳定 Agent 路径是 Codex CLI 的 `codex app-server`。Hermes 和 OpenClaw 使用固定的 ACP
adapter，但尚未作为稳定路径验收。

## 工作方式

```text
发给 Agent 的私聊 -> bot SDK -> Agent turn -> daemon 通过 bot SDK 自动回复

owner 收到托管消息 -> openim-chat business notification -> bot SDK
                  -> daemon 通过 user SDK 读取上下文
                  -> Agent turn（明确告知代表 owner）
                  -> daemon 通过 user SDK 自动回复原会话
```

Agent 只返回本轮最终文本，不选择自动回复的发送身份和目标。处理 hosted turn 时，它知道自己
代表 owner，并在需要查询 owner 资源时使用 `abdim --as user`。

权限不在 CLI 中重复建模。`--as user|bot` 只选择本地 SDK session，OpenIM 服务端根据该
session 的 token 做最终鉴权。项目不使用 MCP、Run grant、私有工具 socket 或动态方法注入。

## 快速开始

要求 Linux 或 macOS、两个不同的 ABD IM 账号，以及已登录且位于 `PATH` 的 Codex CLI。

```bash
go build -o ./abdim ./cmd/abdim
./abdim setup
./abdim daemon status
```

`setup` 会依次登录 owner user 和 Agent bot，保存两个不含 token 的凭据引用，并启动 daemon。
旧的单身份 profile 不兼容此架构，需要重新运行 `abdim setup`。

生命周期命令：

```bash
./abdim daemon start
./abdim daemon stop
./abdim daemon restart
./abdim daemon status
```

## CLI

CLI 默认使用 bot 身份和 JSON 输出。全局 flags 必须放在命令路径之前：

```bash
./abdim --as user user me
printf '%s\n' '{"limit":20}' \
  | ./abdim --as user conversation list --params-stdin
printf '%s\n' '{"conversation_id":"conversation-id","limit":20}' \
  | ./abdim --as user message history --params-stdin
printf '%s\n' '{"recipient_id":"user-id","text":"hello"}' \
  | ./abdim --as bot message send --params-stdin
```

`message send` 用于明确的主动发消息，不用于发送当前 inbound turn 的最终回答。后者由 daemon
根据可信入站引用自动投递。

当前 CLI 支持 profile/user、conversation、message、group/member、friend 和 blacklist
读取，以及文本发送。命令和示例见 [`skills/abd-im`](skills/abd-im/SKILL.md)。Codex run 启动
时，daemon 会把该静态 Skill 安装到工作区的 `.agents/skills/abd-im`。

## 开发

```bash
go test ./...
go vet ./...
```

更多信息：

- [架构](docs/ARCHITECTURE.md)
- [连接器与本地运行](docs/CONNECTOR.md)
- [当前规格](docs/spec.md)
- [重构方案](docs/CLI_DAEMON_SKILLS_REFACTOR_PLAN.md)
- [测试](docs/TESTING.md)

`docs/archive/` 保存历史设计，不代表当前实现。
