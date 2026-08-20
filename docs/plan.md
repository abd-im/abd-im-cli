# 实施计划

当前 daemon/CLI + Skill 重构以
[`CLI_DAEMON_SKILLS_REFACTOR_PLAN.md`](CLI_DAEMON_SKILLS_REFACTOR_PLAN.md) 为准。

## 当前实现

1. 一个 daemon profile 登录 user 和 bot 两个本地 SDK context。
2. direct 与 hosted turn 都由 daemon 自动投递最终文本。
3. CLI 使用 `--as user|bot` 路由到对应 SDK。
4. Codex workdir 安装随版本发布的静态 `abd-im` Skill。
5. grant、proxy、Reply Slot、operation 和动态工具装配已删除。
6. `openim-chat` 只保留托管配置与引用通知，不拉历史、不代发回复。

## 后续独立探索

验证分层 `abdim --help` 是否能以更低维护成本反映当前二进制和服务端权限变化。该探索不与
首期静态 Skill 同时实现，也不引入 MCP 或新的工具目录协议。
