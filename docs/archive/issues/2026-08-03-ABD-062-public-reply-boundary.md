# ABD-062: Configurable Inbound Tool Boundary

状态：`completed`

## Outcome

非 self 的有效私聊默认获得 reply-only run，群聊默认忽略；本机用户可用 `abdim inbound tools enable|disable|status` 为 profile 显式切换全部已验证 typed tools。开关对所有私聊 sender 生效，因此启用模式被明确标记为高信任部署选项。

## Dependencies

已交付的当前用户 runtime、event-bound reply、run-private MCP、typed grant/proxy、capability manifest 和 controlled OpenIM integration tests。

## Acceptance

- 入站 policy 直接接收已解析的 sender、conversation、group 和 session 上下文；公开 policy 只接受非 self 私聊，并以真实 sender 作为 principal。
- reply-only grant 允许空 methods/scopes；启用后的 grant 选择固定 registry 全部方法，以 capability gate 决定 discovery，并使用方法级 typed target 通配、64 次预算和 32 MiB 附件额度。
- 两种 grant 都绑定 run、profile、真实 sender、触发私聊消息窗口和有效期；启用模式可读触发消息之前的同会话历史，不恢复 full-access target/window bypass。
- 默认 provider MCP discovery 为空；启用后只发现 `available` 方法；event-bound reply 正常投递，群聊不创建 run。
- 工具开关写入 profile；修改时重启运行中的 daemon，重新 setup 保留已有设置。
- daemon shutdown 等待 run result 的回复持久化收尾后再关闭控制库。
- controlled OpenIM workflow 显式校验全部 fixtures，并覆盖所有标记 `available` 的 typed read 与 action integration gates；私有 SDK token 缺失时给出可操作错误。
- 活动规格、架构、connector、测试、发布和入口文档描述同一边界。

## Development Record

- 将 policy contract 从持久化 event 扩展为不含消息正文的 `InboundContext`，移除独立 accept hook，避免授权逻辑重复解析未验证事件。
- 删除 grant `FullAccess`、provider `AutoApproveTools` 和 conversation/message handler 的绕过分支；Codex run 固定使用默认审批模式。
- 新增 reply-only、显式 tools policy、配置持久化/重启、方法级通配 target 和触发前历史窗口回归。
- 扩展 GitHub controlled integration 为完整 capability release gate，并同步所有当前文档。
- 验证：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go mod verify`、全部 tagged integration package 只编译检查、四目标 release build、checksum 校验和 `git diff --check` 通过；新增 tools 配置/policy/grant/window 回归连续运行 25 次通过。
- 未执行：本地没有短期 OpenIM fixture，因此未运行真实 controlled integration；`actionlint` 未安装。GitHub 检查确认仓库还没有 `ABDIM_SDK_READ_TOKEN`，`openim-integration` environment 也尚未创建。
