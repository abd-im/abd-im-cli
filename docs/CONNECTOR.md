# Setup and Local Runtime

## Setup

安装并登录 Codex CLI，然后构建和配置 `abdim`：

```bash
codex --version
go build -o ./abdim ./cmd/abdim
./abdim setup
```

`setup` 依次要求 owner user 和 Agent bot 两个不同的 ABD 账号。密码不持久化；两个 OpenIM
token 保存为独立的 `0600` 文件，profile 只记录 credential reference。

默认服务地址：

```text
Account login = https://2.alissa.xin/chat/account/login
OpenIM API    = https://2.alissa.xin/api
OpenIM WS     = wss://2.alissa.xin/msg_gateway
Platform      = 7
```

本地部署可在 setup 时覆盖：

```bash
ABDIM_ACCOUNT_LOGIN_URL=http://127.0.0.1:10008/account/login \
ABDIM_OPENIM_API_ADDR=http://127.0.0.1:10002 \
ABDIM_OPENIM_WS_ADDR=ws://127.0.0.1:10001 \
./abdim setup
```

Chat API 不写入 profile。账号登录使用独立的 login URL，daemon 运行时的普通 IM 能力与托管
上下文均通过两个本地 SDK session 完成。

## Lifecycle

```bash
./abdim daemon start
./abdim daemon status
./abdim daemon restart
./abdim daemon stop
```

不要使用 `sudo`。一个 profile 同时只能运行一个 daemon。

## CLI identities

默认身份是 bot。访问 owner 资源时显式选择 user：

```bash
./abdim --as bot user me
./abdim --as user user me
printf '%s\n' '{"conversation_id":"conversation-id","limit":20}' \
  | ./abdim --as user message history --params-stdin
```

daemon 根据 `--as` 将请求交给对应 SDK context。CLI 不持有 token，也没有额外 grant 或工具
开关；OpenIM 服务端决定所选身份是否有权执行请求。

## Other Agents

profile 接受固定 provider ID：

```bash
./abdim setup --agent hermes
./abdim setup --agent openclaw
```

它们分别使用 `hermes acp` 和 `openclaw acp`。Codex app-server 是当前稳定验证路径。

## Runtime files

```text
<config-dir>/abdim/profiles/<profile>.toml
<data-dir>/abdim/profiles/<profile>/sdk/user/
<data-dir>/abdim/profiles/<profile>/sdk/bot/
<data-dir>/abdim/profiles/<profile>/control.db
<data-dir>/abdim/profiles/<profile>/logs/
<runtime-dir>/abdim/<profile>/daemon.sock
<runtime-dir>/abdim/<profile>/daemon.lock
<runtime-dir>/abdim/<profile>/runs/
```

Codex run 使用独立 workdir，并在其中安装 `.agents/skills/abd-im`。Agent 与 daemon 运行在同一
OS 用户下，因此只能使用可信的本机 Agent。
