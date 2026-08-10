# abd-im-cli

[![CI](https://github.com/abd-im/abd-im-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/abd-im/abd-im-cli/actions/workflows/ci.yml)

**让 ABD IM 成为本机 AI Agent 的消息入口。**

`abdim` 是运行在本机的 ABD IM Agent 连接器。它登录一个 ABD IM 账号，接收私聊和
Agent 工作区消息，将请求交给本机 Agent，并把回答实时送回原会话。无需部署 webhook
或额外的工具服务。

当前主路径为 [Codex CLI](https://github.com/openai/codex)：`abdim` 直接启动
`codex app-server`。Hermes 和 OpenClaw 的 ACP 接口已预留，但尚不属于稳定支持范围。

## 核心能力

- **IM 即入口**：直接在 ABD IM 中发起任务，Agent 的输出始终返回触发消息所在会话。
- **会话隔离**：以 ABD IM 会话 ID 隔离上下文；同一会话内的任务按顺序执行，
  不同会话最多并行处理两个 run。
- **实时回复**：Agent 输出通过流式消息持续回传，无需等待整个任务结束。
- **按需开放 IM 能力**：默认只能回复；显式启用 tools 后，Agent 才能查询消息、发送内容、
  管理群组等。
- **本地运行**：Agent、连接器和运行数据均位于当前机器，daemon 独占 IM 连接与控制数据库。
- **有界操作**：每个 run 使用独立的短期授权、私有 socket、调用预算和消息读取窗口。

## 快速开始

### 准备工作

- Linux 或 macOS
- 一个 ABD IM 账号，建议使用专门的 bot 账号
- 已安装并登录的 `codex` CLI
- Go 1.24 或更高版本（从源码构建时需要）

确认 Codex 可用，然后构建 `abdim`：

```bash
codex --version
go build -o ./abdim ./cmd/abdim
```

> 当前源码构建依赖同级目录中的 `abd-im-sdk-core`。在该 SDK 发布为公共 Go module
> 之前，独立 checkout 无法完成构建。

运行交互式设置。`setup` 会登录 ABD IM、保存当前 profile，并启动后台 daemon：

```bash
./abdim setup
./abdim status
```

现在向该账号发送私聊消息即可触发 Codex。常用生命周期命令：

```bash
./abdim start
./abdim stop
./abdim restart
./abdim status
```

服务地址、自定义 profile 和运行目录等配置见
[连接器指南](docs/CONNECTOR.md)。

## 启用 IM Tools

默认情况下，入站 run 只能回复触发它的会话，不能主动读取或修改其他 IM 数据。
需要完整 IM 能力时显式启用：

```bash
./abdim inbound tools enable
./abdim inbound tools status
```

启用后，`abdim` 会为每个 run 注入私有 CLI、socket、短期授权和固定方法列表。
Agent 可以查询消息与联系人，也可以在授权范围内发送消息、管理会话和群组。无需配置
额外的 MCP 或工具服务。

> **安全提示**：启用 tools 后，所有能够向该 bot 发送受支持消息的账号（包括私聊联系人
> 和 Agent 工作区成员）都可能触发这些能力。当前版本仅适合受控账号和可信的本机 Agent。
> 使用完成后可运行 `./abdim inbound tools disable` 恢复 reply-only 模式。

## 工作原理

```text
     ABD IM
       |
       | inbound message
       v
  abdim daemon
       |
       +-- event ledger -> conversation queue -> Codex app-server
       |                                      |
       |                                      +-- streamed reply
       |
       +-- run-private CLI <-> typed IM methods
```

daemon 是唯一持有 IM 连接、凭据引用和控制数据库的进程。每条入站消息都会创建与
原会话绑定的 reply slot，因此 Agent 不能把回复目标改到其他会话。写操作使用幂等键；
结果不确定时会记录为 `unknown`，而不是自动重试可能产生重复副作用的请求。
Codex 状态按会话持久化；每个 run 仍使用独立进程、工作目录、私有 socket 和短期授权。

更完整的组件职责、权限模型和持久化设计见[架构文档](docs/ARCHITECTURE.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [连接器指南](docs/CONNECTOR.md) | 安装、登录、服务地址与日常运行 |
| [架构](docs/ARCHITECTURE.md) | 组件边界、会话模型、权限与持久化 |
| [产品规格](docs/spec.md) | 当前目标、非目标与验收条件 |
| [测试](docs/TESTING.md) | 单元测试、真实 Codex 与 ABD IM 集成测试 |
| [发布](docs/RELEASING.md) | 版本与发布产物流程 |

`docs/archive/` 仅保存历史设计材料，不代表当前实现。

## 开发

提交变更前运行：

```bash
go test ./...
go vet ./...
```

默认测试不需要真实的 Codex 或 ABD IM 凭据。需要外部服务的测试使用 `integration`
build tag，具体环境变量和命令见[测试文档](docs/TESTING.md)。
