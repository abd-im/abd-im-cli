# 实施计划

## 当前收口

1. Codex 直接接入 `codex app-server`，其他 Agent 继续走 ACP adapter。
2. 保留 conversation queue、event/reply slot、run/operation 持久化。
3. 删除未使用的协议入口、target 授权、manifest/evidence 和持久化 grant 支线。
4. 用真实 Codex、默认测试、vet 和 race test 验证 CLI 工具确实可调用。

## 后续

1. 网页 Agent 工作区读取 conversation/run/operation 数据并提供新会话、列表、取消和状态展示。
2. 需要连续上下文时，以 conversation ID 绑定可恢复的 provider thread；不要改变 IM grant。
3. 逐个验证其他 Agent 的 ACP v1 接入，不为尚未接入的 Agent增加通用插件框架。

任何新设计都应先回答：是否是当前 IM 场景必需，是否比 Multica 更复杂，是否能用
现有 `abdim` CLI 和 daemon 边界完成。
