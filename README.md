# abd-im-cli

安装并登录 Codex CLI 后，在仓库根目录直接编译和配置：

```bash
go build -o ./abdim ./cmd/abdim
./abdim setup
./abdim inbound tools enable
./abdim status
./abdim inbound tools status
```

`setup` 登录 ABD bot 并自动启动当前用户后台 daemon，随后可通过私聊
获得回复。入站默认是 reply-only；`inbound tools enable` 显式开放已验证
的 IM 查询和写入工具。群聊默认忽略；本地 owner 查询通过 CLI 或
`./abdim mcp serve` 完成。日常生命周期使用 `./abdim status`、
`./abdim start`、`./abdim stop` 或 `./abdim restart`。完整流程见
[`docs/CONNECTOR.md`](docs/CONNECTOR.md)。

## 从这里开始

1. [`constitution.md`](constitution.md)：通用工作原则。
2. [`AGENTS.md`](AGENTS.md)：实现行为约束。
3. [`docs/spec.md`](docs/spec.md)：唯一活动产品规格。
4. [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)：组件职责、信任边界与实现状态。
5. [`docs/tasks.md`](docs/tasks.md)：仅包含可分配的活动 task。
6. [`docs/CONNECTOR.md`](docs/CONNECTOR.md)：部署配置、真实 daemon 启动和当前安全限制。
7. [`docs/TESTING.md`](docs/TESTING.md)：默认与 OpenIM integration test 的运行方式。
8. [`docs/RELEASING.md`](docs/RELEASING.md)：GitHub Actions、制品与版本发布流程。

`docs/templates/` 提供 feature 文档骨架，不描述本项目当前状态。`docs/archive/` 仅保存历史材料和已完成/取消的 task，不作为实现依据。为避免双重真相，不保留 `design.md` 或 `issues.md` 别名。
