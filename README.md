# abd-im-cli

`abdim` 把 ABD IM 私聊交给本机 Agent 处理。Codex 直接使用 `codex app-server`；
ACP adapter 仅为后续接入 Hermes、OpenClaw 等其他 Agent 保留。

```bash
go build -o ./abdim ./cmd/abdim
./abdim setup
./abdim inbound tools enable
./abdim status
```

daemon 以 OpenIM `conversation_id` 区分会话，同一会话内的 run 顺序执行。
入站回复始终绑定触发消息所在会话。默认 run 只能回复；启用 tools 后，Agent
通过 run 私有的 `abdim` CLI 调用固定 IM 方法，不需要额外工具配置。

当前入口：

- [`docs/CONNECTOR.md`](docs/CONNECTOR.md)：安装、登录和运行。
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)：当前架构与边界。
- [`docs/spec.md`](docs/spec.md)：精简后的产品要求。
- [`docs/TESTING.md`](docs/TESTING.md)：测试命令。
- [`docs/tasks.md`](docs/tasks.md)：仅保留未完成事项。

`docs/archive/` 只保存历史材料，不作为当前实现依据。
