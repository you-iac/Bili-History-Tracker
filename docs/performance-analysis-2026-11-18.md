# UP 详情吞吐与 CPU 火焰图分析（2026-11-18）

## 结论

本次采样的主要计算热点是**单目标详情复用通用目录查询**，其中还存在一次几乎等量的重复读取。`Content → Catalog` 占进程采样 CPU 的 **96.35%**；详情直接查询和目标状态内部再次查询分别占用 **24.39s、24.48s CPU**。优化应先减少重复查询和单目标查询中的目录聚合、计数，再评估读取连接并发。

当前证据支持保留 HTTP：Go `encoding/json` 调用栈仅覆盖 **0.08s、0.16% CPU**；HTTP 顶层调用栈很宽，主要是包含了下面的数据库执行。CPU 图无法直接量化连接池排队或磁盘等待，需要 block/goroutine/连接池统计补充。

## 数据来源与范围

已直接读取用户提供的 [pprof Top 页面](http://127.0.0.1:18088/ui/top?si=cpu)，解析其内嵌数据，并从页面实际 Download 链接下载原始 CPU profile，再用 `go tool pprof` 的 `-top`、`-peek`、`-list` 和 `-focus` 交叉验证。本报告不只依据截图估算矩形宽度。

| 项目 | 内容 |
|---|---|
| 程序 | `server` |
| Profile Build ID | `1cf30086e4a0846cae4e847404fb30dd30ef6118` |
| 采样开始 | 2026-11-18 17:07:46，北京时间 |
| 采样时长 | 约 30.01s |
| CPU 样本总量 | 50.72s，平均约 1.69 个核的计算时间 |
| 对应负载 | `perf-creator-profile.json`：90s、32 并发、capacity、creator detail |
| 负载时间 | 17:07:09～17:08:40，北京时间；CPU 采样落在该窗口内部 |
| 详情资料池 | 91 个已有完整 UP 资料，随机选择；`page=1&days=30`，未请求历史快照 |
| 本地代码对照 | `24b6be9`；Build ID 本身不能证明 VM 的 Git 提交，尚未独立读取 VM 提交信息 |

50.72s 是多个线程累计采样的 CPU 时间，不表示一次请求耗时 50.72s，也不能据此断言 VM 的 vCPU 数、同一条 SQL 的并行度或 CPU 亲和性。所有 CPU 占比均以 50.72s 为分母，是采样估计。

原始输入与文本分析保存在本地 `backend/bin/profiles/analysis/`，包括 `server-cpu.pb.gz`、`pprof-top.html`、`top-entries.json`、`top-cumulative.txt`、`catalog-callers.txt`、`catalog-source.txt` 和 `json-cpu.txt`。该目录属于已忽略的构建产物，不随本文自动进入版本控制。localhost 页面依赖当前 SSH 隧道和查看器，关闭后不再可访问。

## 吞吐与延迟

| 场景 | 成功请求/s | 成功请求 P50 | P95 | P99 |
|---|---:|---:|---:|---:|
| 之前的 60s UP 详情测试 | 49.07 | 626.17ms | 997.05ms | 1191.58ms |
| 之前的 60s 视频详情测试 | 48.35 | 635.57ms | 1028.70ms | 1199.78ms |
| 本次 90s UP 详情重放 | 39.78 | 771.16ms | 1278.20ms | 1655.06ms |

本次 90s 负载共完成 3612 个请求，全部业务成功，其中 32 个在测量窗口后收尾；窗口内成功数为 3580，因此 `3580 / 90 = 39.78/s`。HTTP 与业务操作一一对应，无限流、500、超时或未完成操作。

采样轮比之前 UP 详情轮吞吐低约 18.9%，但测试时间、时长、后台任务、数据库与宿主机状态没有被严格控制，不能将全部差额认定为 pprof 开销。应另做相同条件下的有/无采样 A/B，对外容量基线使用不采样的重复测试。

## CPU 热点

`flat` 表示函数自身 CPU，`cum` 表示包括子调用的累计 CPU。下表包含嵌套关系，不能将各行百分比相加。

| 调用 | 自身 CPU | 累计 CPU | 累计占比 | 解释 |
|---|---:|---:|---:|---|
| `publicapi.(*Server).clientDetail` | 0.01s | 49.85s | 98.28% | 绝大部分样本来自详情链路 |
| `storage.(*Store).Content` | 0 | 48.87s | 96.35% | 单目标内容读取 |
| `storage.(*Store).Catalog` | 0 | 48.87s | 96.35% | Content 完全委托通用目录逻辑 |
| `modernc.org/sqlite.(*conn).query` | 0 | 49.18s | 96.96% | 包括详情中的其他数据库读取 |
| `_sqlite3VdbeExec` | 20.20s | 44.26s | 87.26% | SQLite 虚拟机执行是主要计算热点 |
| `publicapi.(*Server).targetStatus` | 0 | 24.59s | 48.48% | 主要成本仍是再次 Content |
| `storage.(*Store).Versions` | 0 | 0.62s | 1.22% | 不是本次首要优化对象 |
| `_sqlite3Prepare` | 0 | 4.84s | 9.54% | 包括相关子调用，不能与 VM 执行占比直接相加 |

Top 默认隐藏低占比节点；原始 profile 仍保留这些样本。对整个原始 profile 使用 `-focus=encoding/json`，相关调用栈合计仅 0.08s（0.16%）。这是 Go JSON 的样本，不包含 SQLite 内部 `json_extract` 的执行，也不包含客户端 Python JSON 解析。

`net/http.(*conn).serve` 累计为 98.90%，表示这些 CPU 样本经由 HTTP 请求调用进入；不能将它解读为 HTTP 解析本身消耗了 98.90%。同样，`database/sql.withLock` 的宽度不是锁等待时间，`database/sql.(*DB).retry` 出现在栈上也不能证明实际发生大量重试。

## 已确认的重复工作

### 所属模块与服务的业务

`storage.(*Store).Content` 和 `storage.(*Store).Catalog` 都是服务端 `backend/internal/storage` 包的方法，实现位于 [dashboard.go](../backend/internal/storage/dashboard.go)。`Store` 持有 SQLite 连接；这两个方法为界面和 API 提供本地资料读取模型，在当前服务进程内运行，不是独立网络服务，也不主动访问 B 站。

| 方法 | 业务职责 | 返回内容与边界 |
|---|---|---|
| `Catalog` | 公共库、观察台目录与排行的分页读取 | `ContentPage`，含列表、总数和分页信息；每个 `ContentItem` 合并资料、指标增长、作者投稿统计和跟踪观测状态 |
| `Content` | 按 platform/kind/id 读取单个目标的当前摘要 | 一个 `ContentItem`，用于详情的 item、指标接口资料校验及目标状态组装；不直接返回完整版本列表和指标序列 |

当前 Content 的实现是 `Catalog(Kind, ID, Limit=1)`，所以单目标读取也会带入目录计数和通用聚合；完整详情由上层再组合 `Versions`、`Metrics`、目标状态和可选历史快照。

| 业务入口 | 调用模块与用途 |
|---|---|
| 8181 `GET /creators`、`GET /videos` | `publicapi.clientLibrary → Catalog`，给 Agent/客户端提供公共资料目录 |
| 8181 `GET /` | `clientOverview` 通过 Catalog 读取近期/热门资料，Summary 也会用 Catalog 统计异常目标 |
| 8181 `GET /detail/{type}/{id}` | `clientDetail → Content`，读取详情当前摘要；后续 `targetStatus` 再调用 Content |
| 8181 `GET /api/metrics/{type}/{id}` | `metrics → Content` 校验成功资料，再用 Metrics 读取指标序列 |
| 8181 `GET /api/targets/status` | `targetStatus → TrackingTarget + Content`，组合任务是否存在/启用与资料、观测状态 |
| 8181 `POST /targets` 的最终确认 | `confirmTarget → targetStatus → Content`，用于确认申请后状态；不是这次 CPU 采样的主要业务 |
| 8182 管理观察台 | [admin/pages.go](../backend/internal/admin/pages.go) 的总览、资料库、任务管理、动态、详情和指标等页面复用 Catalog/Content |

公共入口的装配见 [publicapi/server.go](../backend/internal/publicapi/server.go)。本次负载发生器直接请求 8181 的 UP 详情；真实 Agent 则由 [remote.Client.Detail](../client/agent/internal/remote/client.go) 发出相同请求。profile 中主要出现 `publicapi.clientDetail`，说明本次热点由公共详情服务触发，不应将共享 storage 包的全部成本归到 8182 观察台。实时 `/api/v1/platforms/...` 走上游查询链，不是本次 Content/Catalog 热点的来源。

### 本次详情请求的调用证据

[read.go](../backend/internal/publicapi/read.go) 的 `clientDetail` 首先读取 Content，后面调用 [response.go](../backend/internal/publicapi/response.go) 的 `targetStatus`，后者再次读取相同目标的 Content。`-peek` 验证两个分支的累计 CPU 如下：

```text
clientDetail
  +-- Content -> Catalog                   24.39s CPU
  +-- Versions / Metrics / response
  +-- targetStatus                         24.59s CPU
        +-- TrackingTarget                  0.11s CPU
        +-- Content -> Catalog             24.48s CPU
```

两次 Content 合计 48.87s，约各占 Catalog 成本的一半。第二次读取并非微小的状态查询，而是再次进入整个目录查询路径。

此外，[dashboard.go](../backend/internal/storage/dashboard.go) 的 `Content` 只是 `Catalog(Kind, ID, Limit=1)` 的包装。Catalog 每次都查询总数，再执行分页取数。源码行归因显示：

| Catalog 内工作 | CPU 时间 | 占进程采样 CPU |
|---|---:|---:|
| 总数查询及同一源码行的 Scan | 23.23s | 45.80% |
| 分页取数的 QueryContext | 25.52s | 50.32% |
| Catalog 全部工作 | 48.87s | 96.35% |

调用边上的 QueryRowContext 为 23.18s；源码行的 23.23s 还包含同一行 Scan 等成本，两者不能混为不同独立热点。

因此，一个成功且不带 `seq` 的详情请求通常执行约 8 次 SQL：两次 Content 各有计数和取数，共 4 次；版本计数和取数 2 次；指标 1 次；任务状态 1 次。首要问题是这些查询的计算量，不能仅凭 SQL 条数判断成本相同。

## 查询结构与证据边界

Catalog 的 CTE 同时构造视频、UP 和任务占位记录，包含作者投稿计数、播放量汇总、最近投稿以及指标窗口的分组和关联。虽然最终只查一个 ID，仍复用了全功能目录的统计、排序与分页模型。

SQL 文本中的 `windows` 在平台和时间范围内聚合指标，目标 ID 的约束在后面的过滤中。是否由 SQLite 优化器下推、具体扫描多少行、有无临时 B-tree，必须在实际测试库上对计数与取数分别执行 `EXPLAIN QUERY PLAN`，不能由 CPU 火焰图直接宣布“全表扫描”。B-tree 的 balance/insert 热点也可能来自临时结构，不能解释成读取请求修改了业务资料。

仓库迁移已定义目标主键、`idx_metrics_window` 和 `idx_videos_creator_published` 等索引，见 [初始迁移](../backend/internal/storage/migrations/0001_init.sql) 与 [观测状态迁移](../backend/internal/storage/migrations/0004_observation_status.sql)。不能未经执行计划检查，就把问题归为“没有索引”。

[db.go](../backend/internal/storage/db.go) 明确设置 `SetMaxOpenConns(1)`，并发请求可能在单连接前排队。但当前取得的是 CPU profile，没有本轮 block/trace、连接池 `WaitCount/WaitDuration` 或磁盘监控，因此不能计算一秒响应里有多少比例在等待连接、磁盘或调度。

## 优化建议与复测

1. **先移除详情中的重复 Content。** 详情已有的 ContentItem 可供状态组装复用，仍需读取真实任务状态并检查运行状态。保留 UP `Seq=0` 对应 `recorded=false`、无任务、暂停、取消和运行中的语义，参照 [公共应答协议](public-response.md)。不要直接用较早读取的 Following 字段代替当前任务确认。
2. **让单目标 Content 使用专用查询。** 按 `platform + kind + id` 定位目标，不再为了最多一条资料调用完整 Catalog，不查询目录总页数。指标和作者投稿统计限定到当前目标；目录接口继续保留自己的筛选、排行和分页职责。
3. **按实际执行计划调整聚合、排序与索引。** 分别测计数和取数成本，再考虑预编译或语句复用。当前 prepare 有成本，但执行 CPU 明显更大，单独缓存语句不能消除主要执行工作。
4. **最后评估读取池与写入连接分离。** 先降低每次请求计算量，再用同一数据库和并发阶梯验证读连接数的收益。保持 SQLite 写入约束，并观察采集写入、锁竞争与错误率。

仅从采样构成看，第二次 Content 约占全进程 48.26% CPU，说明有很大的减少工作量空间。但这不是承诺“吞吐立刻翻倍”：CPU、等待、缓存与并发行为会一起变化；删除重复读取和取消 Catalog 计数的收益也有重叠，不能将两个百分比直接相加。

建议每项改动单独复测 creator/video 的 1、4、8、16、32 并发，固定数据快照、采集器状态和限流配置，记录业务 goodput、P50/P95/P99、错误、每请求查询次数、CPU、连接等待和磁盘指标。采一轮独立 block profile，验证连接等待栈；优化后再采同条件 CPU 图，确认 Content/Catalog 和计数热点实际下降，而非移到另一个包装函数名下。

当前应以减少查询工作量为优先，无需先更换 HTTP、自定义通信协议、JSON 库或 SQLite 驱动。

## 复现分析命令

在仓库根目录，用已保存的原始 profile 执行：

```bash
go tool pprof -top -cum -nodecount=25 backend/bin/profiles/analysis/server-cpu.pb.gz
go tool pprof -peek 'storage.*Catalog|storage.*Content|publicapi.*targetStatus' backend/bin/profiles/analysis/server-cpu.pb.gz
go tool pprof -top -focus='encoding/json' backend/bin/profiles/analysis/server-cpu.pb.gz
```

源码归因需要匹配采样构建的源码路径；本次以本地工作区配合 `-source_path` 和裁剪 VM 根路径的 `-trim_path` 完成。本文未修改业务代码，也未在用户数据库执行查询计划或索引变更。
