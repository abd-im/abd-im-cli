# abdim 当前规格

## 目标

`abdim` 在一个本机 daemon 中维护 owner user 和 Agent bot 两个 SDK 身份，把 ABD IM 消息交给
本机 Agent，并把最终文本自动发回可信入站引用指向的会话。

## 要求

1. 一个 profile 同时登录隔离的 user SDK 和 bot SDK，且 user ID 必须不同。
2. bot 普通私聊触发 direct turn；bot business notification 触发 hosted turn；user 普通 callback 不触发 turn。
3. hosted notification 必须包含 `owner_user_id`、conversation 和 trigger 引用；daemon 校验 owner 后通过 user SDK读取上下文。
4. Agent prompt 明确 direct/hosted、当前代表身份和应使用的 `--as`。
5. Agent 只返回 final text；direct 由 bot SDK 自动发送，hosted 由 user SDK 自动发送。
6. 同一 `identity:conversation_id` 串行，不同 conversation 有界并行。
7. CLI 的 `--as user|bot` 只选择本地 SDK；服务端按对应 token 做最终鉴权。
8. 人和 Agent 共用普通 CLI，不使用 MCP、grant、Run-private socket、动态方法目录或 Reply Slot。
9. Codex run 原生发现随版本发布的静态 `abd-im` Skill。
10. 只持久化入站去重和 provider session；不恢复或补发崩溃中的旧 turn。

## 非目标

- 不实现 task、claim、complete/fail 模型。
- 不包装 SDK 的全部接口；只暴露当前已验证的 IM 命令。
- 首期不实现分层 `abdim --help` 权限发现。
- 不用本地 CLI 权限层替代 OpenIM 服务端鉴权。
- 不把同一 OS 用户下运行的 Agent 当作恶意进程隔离。

## 验收

- user/bot SDK 可同时启动、分别路由 CLI 调用并按相反顺序关闭。
- direct 和 hosted 可见发送者正确，消息不串 identity 或 conversation。
- hosted 历史读取和最终回复不经过 `openim-chat` 业务代发 API。
- Skill 安装到 Codex workdir，且不包含动态权限或不存在的命令。
- `go test ./...`、`go vet ./...` 和两个仓库的相关测试通过。
