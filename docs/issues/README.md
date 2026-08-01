# Active Issue Records

[`../tasks.md`](../tasks.md) 是活动 issue 的唯一索引和状态入口。本目录只保存活动 issue 的当前范围、验证条件和已经发生的开发记录；实现所有权由 [`../ARCHITECTURE.md`](../ARCHITECTURE.md) 的能力领域映射统一说明。

实现开始时再创建 issue record，并在其中写入实现决策、验证结果和完成 commit；随后将 record 移入 `../archive/issues/YYYY-MM-DD-<issue-id>-<slug>.md`，并将活动 task 移入同日的 task archive。未开始的 task 只保留在 `../tasks.md`，不创建空 issue 文件。
