# 裁剪视频快照，过滤合集中的其他视频数据

## 完成摘要

- 状态：已完成。

**裁剪视频快照，过滤合集中的其他视频数据**（创建时间：2026-11-07；完成时间：2026-11-08，历史记录均仅确认日期；提交：`afcd74e`）。已在 [B站适配器](../backend/internal/provider/bilibili/snapshot.go) 中仅过滤顶层 `ugc_season`，保留当前视频全部分 P、统计、字幕及未知字段；过滤规则版本仅在模块代码中选择，不随数据传递或入库，通过 [0003 迁移](../backend/internal/storage/migrations/0003_drop_video_snapshot_version.sql) 删除多余列并兼容旧库，保持内容指纹、版本追加规则和历史 payload 不变。快照过滤、入库与详情读取、schema 1/2 升级及完整性测试通过，`go test ./...`、`go vet ./...` 和服务端构建通过；历史快照裁剪与空间回收不在本项范围。字段图例见 [数据库设计](../docs/database.md#视频快照裁剪前后对照)，验证见 [快照测试](../backend/internal/provider/bilibili/snapshot_test.go) 和 [迁移测试](../backend/internal/storage/migration_test.go)。

## 历史记录说明

本计划从原集中计划文件迁入；迁移前仅保留以上完成摘要，未补造原始方案。摘要中的创建时间、完成时间、验证结果及边界均沿用历史记录。
