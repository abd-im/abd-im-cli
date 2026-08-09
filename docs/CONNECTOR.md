# Setup and Local Runtime

## Codex

先安装并登录 Codex CLI，确认 `codex` 位于 `PATH`：

```bash
codex --version
go build -o ./abdim ./cmd/abdim
./abdim setup
```

`setup` 默认登录当前 ABD 部署，保存当前用户私有的 profile/token，并启动 daemon。
不要使用 `sudo`。密码不持久化，token 文件权限为 `0600`。

```text
Account login = https://2.alissa.xin/chat/account/login
OpenIM API    = https://2.alissa.xin/api
Chat API      = https://2.alissa.xin/chat
OpenIM WS     = wss://2.alissa.xin/msg_gateway
Platform      = 7
```

可在执行 `setup` 时通过环境变量覆盖服务地址。OpenIM API、Chat API 和 WebSocket 地址会保存到
profile，后续执行 `start` 或 `restart` 时不需要重复传入：

```bash
ABDIM_ACCOUNT_LOGIN_URL=http://127.0.0.1:10008/account/login \
ABDIM_OPENIM_API_ADDR=http://127.0.0.1:10002 \
ABDIM_CHAT_API_ADDR=http://127.0.0.1:10008 \
ABDIM_OPENIM_WS_ADDR=ws://127.0.0.1:10001 \
./abdim setup
```

日常命令：

```bash
./abdim status
./abdim restart
./abdim stop
./abdim start
```

## IM Tools

默认入站 run 是 reply-only。需要 Codex 查询或修改 IM 数据时显式启用：

```bash
./abdim inbound tools enable
./abdim inbound tools status
./abdim inbound tools disable
```

daemon 自动向每个 run 注入 `ABDIM_CLI`、私有 socket、grant 和方法快照。
Agent 可直接运行：

```bash
"$ABDIM_CLI" commands
printf '%s' '{"conversation_id":"conversation-1","limit":20}' \
  | "$ABDIM_CLI" message history --params-stdin
```

不需要配置额外工具服务。命令、文件和网络能力由 Codex app-server 正常提供；
IM 调用仍必须经过 `abdim` 的 run grant。启用 tools 后会对所有能私聊该 bot 的
账号生效，因此当前版本只适合受控 bot 账号。

## Other Agents

profile 仍接受固定 ID：

```bash
./abdim setup --agent hermes
./abdim setup --agent openclaw
```

它们分别使用 `hermes acp` 和 `openclaw acp`。这部分是后续接入点；当前主路径和
真实集成验证只保证 Codex app-server。

## Runtime Files

```text
<config-dir>/abdim/profiles/<profile>.toml
<data-dir>/abdim/profiles/<profile>/{sdk,control.db,attachments,logs}/
<runtime-dir>/abdim/<profile>/{daemon.sock,daemon.lock,runs/}
```

owner CLI 连接已运行 daemon，不自行初始化 SDK。Agent 和 daemon 运行在同一 OS
用户下，所以应只选择可信的本机 Agent。
