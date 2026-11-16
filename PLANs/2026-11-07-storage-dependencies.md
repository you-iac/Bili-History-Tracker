# 评估并优化部分 `storage.Store` 依赖

- 创建时间：2026-11-07（历史记录仅确认日期）。
- 状态：待评估。
- 检查 `tracker.Tracker`、`tracker.Scheduler` 和 `admin.Server` 对 `*storage.Store` 的依赖，按各自实际需要的能力评估是否改为小接口。
- 优先评估只使用 `DueTargets`、`MarkTargetRun` 的调度器；结合收益再确定其他组件的调整范围。
- 检查 `storage.Target` 等共享类型的职责，区分调度数据与后台展示数据，避免仅替换接口却继续混合职责。
- 保留入口 `cmd/server` 对具体存储实现的创建和组装，以及当前记录、历史版本和版本指针的事务一致性。
- 评估后确定实施范围，再同步调整必要的测试、模块注释和技术文档。
