# 详情查询优化与双库列表性能

本文保留第一轮详情查询优化与列表问题证据。随后客户端已改用本地当前资料、批量历史与轻量目录；当前职责和请求成本见 [本地优先资料](local-first-metadata.md)，不再用逐项目录刷新关注卡片。

## 范围

2026-11-19 优化视频/创作者共用的本地详情读取，核查公共视频库、UP 主库与 Agent 本地关注列表的服务器开销。以下测量全部使用临时合成数据库，不读取用户数据库、不访问 B站或 VM；不代表实际服务器吞吐与页面端到端延迟。

此前的 [2026-11-18 CPU 分析](performance-analysis-2026-11-18.md) 保留为优化前的采样证据。实施与验证记录见 [已归档的详情查询计划](../PLANs/archive/2026-11-19-detail-query-optimization.md)。

## 已实现的详情优化

[storage/content.go](../backend/internal/storage/content.go) 的 `Content` 按 `platform + kind + id` 读取单个视频或作者，指标窗口同时限定目标、指标和时间。它不再委托目录 `Catalog`，不计算目录总数，不构造其他类型的资料池。作者投稿统计仍限定于当前作者，保留视频总数、已记录播放量合计与近 7 个 UTC 日投稿数。

`Catalog` 与 `Content` 共用 `contentEnrichmentCTE`、`scanContent` 和 `ContentItem.ObservationStatus`，保持未知值、窗口边界、当前图片版本及观测状态口径一致。不存在资料仍返回 `sql.ErrNoRows`；视频引用产生的作者占位行仍为 `Seq=0`，公共协议不将其视为已成功记录的完整作者资料。

[publicapi/read.go](../backend/internal/publicapi/read.go) 的 `clientDetail` 只读取一次 Content。[response.go](../backend/internal/publicapi/response.go) 的 `targetStatusWithContent` 复用请求内摘要，仍重新查询任务存在/启停和 tracker 运行状态；暂停、取消和恢复不能由较早的 Following 字段推断。观测计数、失败与恢复信息来自本次请求第一次读取的摘要，不提供跨多个 SQL 的事务快照，也不跨请求缓存。无资料的详情和指标应答也复用已知缺失结果，并沿用原请求的超时上下文。

成功且不带 `seq` 的公共详情由原来的约 **8 次 SQL 减少到 5 次**：Content 1 次、版本计数与取数 2 次、指标 1 次、实时任务确认 1 次。读取指定快照再加 1 次 SQL。仅创建了任务但没有资料的目标不会因为该优化被隐式采集。

## 隔离数据基准

可复现夹具和命令位于 [content_benchmark_test.go](../backend/internal/storage/content_benchmark_test.go)。夹具包含 128 位完整作者、1024 个视频、8064 条 7 日指标、每个目标各一条版本和一条任务；迁移后执行 ANALYZE。数据库和文件随测试临时目录清理，准备数据不计入测量。

环境：Windows/amd64，Intel Core i7-14650HX，Go 1.26；沿用单连接 SQLite。单线程调用、热缓存，`-benchtime=300ms -count=3`；下表为三轮中位数，未控制其他进程负载。基准名中的 `-24` 是 GOMAXPROCS 标记，不表示 24 个并发请求。

| 单次摘要读取 | 原 Catalog 路径 | 新 Content | 耗时比 |
|---|---:|---:|---:|
| 视频 | 7.214 ms | 0.729 ms | 原路径约为新的 9.9 倍 |
| 作者 | 8.320 ms | 0.914 ms | 原路径约为新的 9.1 倍 |

这只比较摘要读取。完整详情还需要版本、指标和任务确认，不能将该比例当成页面加速比或 VM 容量增长倍数。

## 列表已确认的服务器开销

### 目录计数仍携带通用聚合

[dashboard.go](../backend/internal/storage/dashboard.go) 的 `Catalog` 每次先用完整 `catalogCTE + enriched` 计算总数，再执行分页取数。`windows` 在平台和近 7 日范围内聚合 view/follower，未按当前目录类型限制；普通按观测时间排序和精确 ID 摘要请求也经过此窗口。当前新 Content 绕开了这条路径，列表本身仍保留旧实现。

隔离库的计数执行计划确实出现 `MATERIALIZE windows`、`SEARCH metric_samples USING PRIMARY KEY (platform=?)`，而非只按目标定位。计数并未因为返回一个整数就消除指标关联。真实库的数据分布、历史跨度和统计信息可能使 SQLite 选择不同计划，需要在 VM 一致快照上再次核对。

| 18 条列表首页 | Catalog 整体，含计数 | 单独计数 |
|---|---:|---:|
| 视频，默认观测时间排序 | 17.381 ms | 8.077 ms |
| 视频，热门增量排序 | 14.913 ms | 7.218 ms |
| 作者，默认观测时间排序 | 9.559 ms | 4.658 ms |
| 作者，热门增量排序 | 10.125 ms | 5.534 ms |

计数是另一次独立基准，不能通过相减精确分解整页成本，但足以说明它不是可以忽略的工作。

### 分页之前的统计和排序

通用资料池包含视频、作者及任务占位分支；作者投影有四个相关子查询，分别统计视频数、最近投稿、播放合计和窗口内投稿数。计划可见这些分支和相关子查询；计划中出现某个分支本身不能证明它在运行时对每一行都执行。

默认观测排序、热门增量排序的分页核心计划都出现 `USE TEMP B-TREE FOR ORDER BY`。`LIMIT 18` 因而不保证只处理 18 条候选记录。增长类排行需要先计算候选目标的增长；非增长排序也沿用通用指标聚合，是后续应拆开的工作。

图片 JSON 提取已经放在分页之后，没有把整份 payload 带入目录排序；不应将所有成本误归为“列表加载了全部快照”。现有 `idx_videos_creator_published` 等索引已被投稿统计使用，不能未经执行计划就归因于缺少索引。

### 卡片图片引发额外详情请求

`Catalog` 已提取 Cover，但当前 [clientItem](../backend/internal/publicapi/read.go) 的公共字段映射没有返回 cover。[remote.Item](../client/agent/internal/remote/client.go) 和 [images.js](../client/frontend/images.js) 支持直接使用已知图片地址，当前缺失地址时会走 Agent 图片元数据解析。

[library/images.go](../client/agent/internal/library/images.go) 的 `readImage` 在缓存未命中时调用完整 Detail 获取所选快照，其中包含不属于图片解析必需的趋势和版本列表。已知正版本号时调用一次；`seq=0` 时先查询当前版本，再请求该版本快照，最多调用两次。

例如 18 张可见卡片均有已知版本且图片地址未缓存时，除目录请求外还可能发出 18 个完整详情请求。当前实现已有可见区域懒加载、浏览器最多 4 个元数据请求、Agent 图片并发槽及地址缓存，因此这不是每次翻页必定发生的请求量。图片文件由浏览器直连白名单 CDN，服务器承担的是元数据查询，不是图片内容传输。

### 本地摘要同步逐项请求目录

[library/summaries.go](../client/agent/internal/library/summaries.go) 的 `SyncSummaries` 遍历当前页 ID，逐项调用 `/videos?id=...` 或 `/creators?id=...`，每项仍触发 Catalog 的计数与取数；浏览核对还可能先查询目标状态，明确缺失时再申请跟踪。初始卡片来自本地数据，但后台资料回填会产生远程请求。

同步有最多 24 项、20 秒预算和相同目标集合在途合并；[library.js](../client/frontend/library.js) 对同一键有 60 秒抑制。现有约束能控制请求速率，不能消除 N 项对应 N 次目录读取。此次单目标 Content 优化改善了状态/详情读取，没有让精确 ID 的目录摘要自动改走新查询。

### 单连接放大交叉影响

[db.go](../backend/internal/storage/db.go) 仍为 `MaxOpenConns(1)`，所有目录、详情和采集写入共用同一队列。列表昂贵时会占用同一个可用连接，影响其他业务的等待时间。本次未测连接等待占比、锁等待或 VM 磁盘指标，不据此声称它们占用了某个百分比。

## 后续优化顺序

1. 让公共摘要返回已提取且符合现有图片白名单的 cover，或提供轻量图片元数据读取；保留选定版本、未知地址及当前缓存规则，避免每张图片请求整份详情。
2. 精确 ID 摘要复用专用 Content，或增加有界批量摘要；本地关注同步继续保留身份校验、代际隔离与有界请求。
3. 按筛选条件拆开轻量计数、非增长排序与增长排行；在分页后补充不影响筛选/排序的指标和作者统计。热门排序仍需覆盖完整候选集，不能只在当前页排序。
4. 固定真实数据库快照、采集状态和限流配置，测 creator/video 列表与详情的并发阶梯、goodput、P95/P99、CPU 与连接等待，再评估索引、汇总缓存或读取连接池。

包含式搜索和 OFFSET 深分页也需要单独测量；当前源码可确认这些访问方式，但本轮没有量化它们在真实库上的贡献。

## 复现与回归

在 backend 目录运行：

```powershell
go test ./internal/storage ./internal/publicapi
go test -run '^TestReadQueryPlans$' -v ./internal/storage
go test -run '^$' -bench 'Benchmark(ContentLookup|CatalogList)$' -benchtime=300ms -count=3 -benchmem ./internal/storage
```

`TestReadQueryPlans` 检查单目标完整查询、目录原始计数与分页前核心查询；后者不含分页之后的图片关联，不能用于推断图片读取成本。计划日志用于诊断，不锁定 SQLite 的具体节点名称。字段、只读与窗口回归见 [content_test.go](../backend/internal/storage/content_test.go)，一次资料读取及实时任务状态回归见 [detail_query_test.go](../backend/internal/publicapi/detail_query_test.go)。

本轮 Windows 后端/客户端完整 Go test、vet 和构建通过；前端 lint、17 项前端测试及 3 项后台图表测试通过。未执行真实 VM 压测、Linux 运行或浏览器端到端验收。
