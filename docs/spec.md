# abdim 当前规格

## 目标

`abdim` 在本机 daemon 中连接 ABD IM 和 Agent。当前主路径是 Codex
app-server；其他 Agent 将来通过保留的 ACP adapter 接入。实现应保持接近 Multica
的直接连接方式，不增加额外协议层和权限配置层。

## 当前要求

1. 以 OpenIM `conversation_id` 区分会话，回复不能串到其他会话。
2. 同一会话的 run 顺序执行，不同会话最多并行两个 run；run 状态可持久化、查询和取消。
3. Codex 直接使用 `codex app-server`，不能依赖 ACP wrapper。
4. ACP adapter 保留给后续其他 Agent，但不影响 Codex 主路径。
5. Agent 通过 run-private `abdim` CLI 读取或修改 IM 数据。
6. 默认 reply-only；显式启用 tools 后，所有固定 IM 方法必须能够正常调用。
7. 权限只保留 run/profile/method/expiry/budget、消息窗口和附件额度。
8. 每次回复绑定触发 event 的 reply slot，Agent 不能指定回复目标。
9. 写操作使用 idempotency key；结果不确定时记录 `unknown`，不自动重试。
10. 保存 future web workbench 所需的 conversation、event、run 和 operation 标识。

## 非目标

- 不实现任意 provider/plugin 命令。
- 不实现复杂 target allowlist、运行时证据矩阵或多层 manifest gate。
- 不把 Agent 当作不可信的同 UID 恶意进程隔离。
- 当前不实现网页工作区，也不实现 Codex thread 跨消息复用。
- 不在 server 或 SDK 中加入 Agent 专用协议。

## 权限模型

- Codex app-server 允许正常使用命令、文件和网络工具。
- IM 方法只能从 daemon 的固定 registry 选择。
- run proxy 在调用时检查 grant credential、run、profile、method、过期和预算。
- message service 额外限制当前 conversation 和触发消息之前的读取窗口。
- handler 负责输入 schema、业务状态、附件额度和 operation 幂等。

## 验收

- 两个 conversation 的 event、run、queue 和 reply slot 不混用。
- 同一 conversation 的多个 run 保持顺序。
- 两个不同 conversation 可以同时执行，profile 总并发不超过两个 run。
- 真实 Codex 能启动 app-server、执行 `abdim commands` 并调用授权 IM 方法。
- tools disabled 时命令列表为空但仍能回复；enabled 时固定方法可调用。
- provider 不能调用 owner 的 run/operation 管理方法。
- 取消、grant 过期或 daemon 关闭后不能继续调用该 run 的 IM 方法。
- `go test ./...`、`go vet ./...` 和聚焦 race tests 通过。
