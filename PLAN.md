# 项目计划

待办保留详细方案，新建计划放在待办区最上面；完成后压缩为一段，追加到文件末尾归档。时间统一使用北京时间（Asia/Shanghai）；历史记录只确认日期时不补写具体时分。

## 待办计划

### 解耦 data 指纹操作

- 创建时间：2026-11-07（历史记录仅确认日期）。
- 状态：待实施。
- 将 `backend/internal/storage/fingerprint.go` 中 B 站 `data` 的字段投影、规范化和指纹生成逻辑移到 `backend/internal/provider/bilibili`，由哔哩哔哩适配器决定哪些数据参与指纹。
- 查询结果携带指纹及其算法版本，`tracker` 负责协调查询和保存，`storage` 接收结果并完成比较、版本写入与事务处理，不再解析 B 站原始字段或决定指纹规则。
- 保留现有内容变化判断：粉丝数、播放量等统计变化不产生内容版本；头像、封面 URL 的参数变化不误判为内容变化。
- 迁移时核对现有指纹兼容性，并明确算法版本变化时的比较策略；同步调整相关测试、模块注释和技术文档。

### 评估并优化部分 `storage.Store` 依赖

- 创建时间：2026-11-07（历史记录仅确认日期）。
- 状态：待评估。
- 检查 `tracker.Tracker`、`tracker.Scheduler` 和 `admin.Server` 对 `*storage.Store` 的依赖，按各自实际需要的能力评估是否改为小接口。
- 优先评估只使用 `DueTargets`、`MarkTargetRun` 的调度器；结合收益再确定其他组件的调整范围。
- 检查 `storage.Target` 等共享类型的职责，区分调度数据与后台展示数据，避免仅替换接口却继续混合职责。
- 保留入口 `cmd/server` 对具体存储实现的创建和组装，以及当前记录、历史版本和版本指针的事务一致性。
- 评估后确定实施范围，再同步调整必要的测试、模块注释和技术文档。

## 已完成计划

**裁剪视频快照，过滤合集中的其他视频数据**（创建时间：2026-11-07；完成时间：2026-11-08，历史记录均仅确认日期；提交：`afcd74e`）。已在 [B站适配器](backend/internal/provider/bilibili/snapshot.go) 中仅过滤顶层 `ugc_season`，保留当前视频全部分 P、统计、字幕及未知字段；过滤规则版本仅在模块代码中选择，不随数据传递或入库，通过 [0003 迁移](backend/internal/storage/migrations/0003_drop_video_snapshot_version.sql) 删除多余列并兼容旧库，保持内容指纹、版本追加规则和历史 payload 不变。快照过滤、入库与详情读取、schema 1/2 升级及完整性测试通过，`go test ./...`、`go vet ./...` 和服务端构建通过；历史快照裁剪与空间回收不在本项范围。字段图例见 [数据库设计](docs/database.md#视频快照裁剪前后对照)，验证见 [快照测试](backend/internal/provider/bilibili/snapshot_test.go) 和 [迁移测试](backend/internal/storage/migration_test.go)。

**后台界面重构：B站关注管理与信息看板**（创建时间：2026-11-08 20:41；完成时间：2026-11-08 21:40）。已将 8182 后台重构为“Bili 观察台”：信息总览、视频库、UP 主库、动态与提醒、对象详情、关注管理、采集记录七个页面，配套两个只读 JSON 接口与内嵌 CSS/JS；页面只读本地，只有命令异步触发 `tracker.RunNow`。存储侧新增只读看板查询（概览、分页筛选、日指标、版本动态、增长与快照），列表不返回整份 payload；0004 迁移为 `tracking_targets` 增加最近结果、最近成功时间、连续失败与恢复时间并回填旧目标，看板据此区分正常、失败、较旧、已恢复与待采集，`running` 仅表示本进程在途查询。图表使用内联 SVG，缺失日切断、单点不造增长、回落保留负值，并提供可展开数据表。验证通过 `go test ./...`、`go vet ./...`、`node --test internal/admin/chart_test.cjs` 及页面与接口手动检查；页面与状态口径见 [观察台设计](docs/dashboard.md)，迁移见 [数据库设计](docs/database.md)。
