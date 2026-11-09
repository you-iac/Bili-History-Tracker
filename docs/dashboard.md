# 观察台：页面、数据口径与接口

8182 观察台的产品化重构。常规内容与统计页面只读本地 SQLite；作者可添加视频列表可只读发现平台视频，列表本身不写入内容或关注；添加、刷新、停止关注等命令仍走 POST，由 `tracker` 异步执行。代码入口：[pages.go](../backend/internal/admin/pages.go)、[views.go](../backend/internal/admin/views.go)、[server.go](../backend/internal/admin/server.go)、[dashboard.go](../backend/internal/storage/dashboard.go)；模板与静态资源在 [templates](../backend/internal/admin/templates) 和 [assets](../backend/internal/admin/assets)。

## 信息架构

```text
┌──────────────────────────────────────────────────────────────┐
│ 侧栏                                                          │
│  信息看板：信息总览 / 视频库 / UP 主库 / 动态与提醒            │
│  工作空间：关注管理 / 采集记录                                 │
└──────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│ 列表与概览     │     │ 对象详情       │     │ 管理与日志     │
│ 只读聚合       │     │ 指标/版本/快照 │     │ 命令 + 本地日志│
└───────────────┘     └───────────────┘     └───────────────┘
```

常规浏览不触发采集；“可添加视频”经 discovery 只读发现，勾选提交或添加全部才采集入库。作者关注只查询资料，不自动添加视频。

## 路由

| 方法与路径 | 页面 | 数据来源 |
|---|---|---|
| `GET /` | 信息总览 | `Summary`、`Catalog`、`Versions`、`CollectionSeries` |
| `GET /videos` | 视频库 | `Catalog`（kind=video） |
| `GET /creators` | UP 主库 | `Catalog`（kind=creator） |
| `GET /activity` | 动态与提醒 | `Versions`（updates）或 `Catalog`（issues/recovered） |
| `GET /manage` | 关注管理 | `Catalog`（OnlyTargets） |
| `GET /logs` | 采集记录 | 内存日志 `Recent(500)` |
| `GET /detail/{video\|creator}/{id}` | 对象详情 | `Content`、`ReadSnapshot`、`Metrics`、`Versions`、`Catalog`（作者视频） |
| `GET /api/metrics/{type}/{id}?days=` | 趋势 JSON | `Metrics` |
| `GET /api/targets/status` | 关注状态 JSON | `Catalog` + 在途状态 |
| `GET /assets/*` | 样式与脚本 | `go:embed` |
| `POST /targets` | 添加关注 | `AddTarget` + `RunNow` |
| `POST /targets/delete` | 停止关注 | `RemoveTarget`，保留历史 |
| `POST /targets/run` | 立即刷新 | `RunNow` |
| `POST /targets/interval` | 修改间隔 | `SetTargetInterval` |

添加关注支持 BV、UID，以及可本地解析的标准视频/空间链接；短链和其它 URL 不解析、不传给上游。重复添加按 `(platform, type, id)` 去重，并提示“已关注该目标，已更新设置”。

## 页面职责

- **信息总览**：已记录视频/UP 主、正在关注、近 7 日新增版本、需关注问题；最近 14 日累计记录趋势；关注范围内的 UP 主与视频排行（粉丝/播放，或近 7 日增量）；最近内容动态和问题摘要。
- **视频库 / UP 主库**：封面或头像、名称、作者、指标、最近观测、版本数、状态；搜索、排序、关注状态筛选、分页，每页 18 条。
- **动态与提醒**：`updates` 展示 `seq > 1` 的版本变化（首次收录单独处理），支持 7/30/90/全部和类型筛选；`issues` 展示采集异常与数据较旧；`recovered` 展示近期恢复。
- **对象详情**：摘要、指标趋势（7/30/90/全部）、自首次记录以来的增量、版本时间线、相邻版本内容字段对比、采集快照（默认收起）、B站原链接；UP 主详情另有“已记录投稿”，每页 6 条。
- **关注管理**：添加、当前关注列表（含未成功采集的目标）、间隔、立即刷新、停止关注；失败目标显示原因，不显示为已删除。
- **采集记录**：内存最近 500 条，重启时从日志文件末尾 2 MiB 恢复；按结果、类型、关键词筛选，每页 25 条。

## 状态与问题口径

`ContentItem.Status` 在 [dashboard.go](../backend/internal/storage/dashboard.go) 的 `scanContent` 中按固定顺序判定：

| 顺序 | 条件 | 状态 | 页面文案 |
|---|---|---|---|
| 1 | 不在启用的关注中 | `unfollowed` | 已记录 |
| 2 | `last_result` 非空且不是 `ok` | `error` | 查询失败 |
| 3 | `last_success_at = 0` | `pending` | 等待采集 |
| 4 | 距最近成功超过阈值 | `stale` | 数据较旧 |
| 5 | `recovered_at > 0` | `recovered` | 已恢复 |
| 6 | 作者、`seq = 0`、且已停止关注 | `profile_pending` | 资料待采集 |

- 阈值：`max(间隔 × 2, 30 分钟) + 30 分钟`，即允许一次周期抖动和一次宽限。
- `running` 只来自本进程的在途查询（`Tracker.IsRunning`），不持久化，避免重启后遗留“采集中”。
- `issues` 条件：正在关注，且 `last_result` 非空非 `ok`，或 `last_success_at` 早于阈值。
- `recovered` 条件：正在关注、`last_result = ok`、`recovered_at` 在窗口内，且当前不再满足 `issues`。
- 失败只代表采集异常，不代表视频已删除、下架或不可播放；完整可访问性状态机仍属后续能力。

## 统计与指标口径

| 指标 | 口径 |
|---|---|
| 已记录视频 / UP 主 | `videos` / `creators` 当前表行数 |
| 正在关注 | `tracking_targets` 中 `enabled = 1` |
| 近 7 日更新 | `video_versions` + `creator_versions` 中 `seq > 1` 且 `observed_at >= now - 7d` |
| 需关注问题 | `issues` 查询的总数，不是单次失败次数 |
| 记录规模 | 最近 14 日按 `first_seen_at` 的 UTC 日桶累计，非播放量插值 |
| 增长 | 近 7 日窗口内至少 2 个有效采样，取首末差值；单点或缺失返回“样本不足” |
| 排行 | 仅在本地已记录/正在关注范围内排序，不是 B站全站榜 |

指标来自 `metric_samples`：

- `sampled_at` 是 UTC 日窗口起点，不是抓取时刻；趋势图明确标注 UTC 采样日，操作时间显示北京时间。
- 支持 7/30/90 日和全部；全部最多返回最近 5000 点，超出时 `truncated = true`。
- 缺失日不补零、不插值；计数回落保留真实负值。
- 首版沿用现有采样粒度，不因展示需求新增每次查询的历史行。

列表和卡片只从最新快照提取封面（`json_extract(v.payload, '$.pic')` / `$.card.face`），不在列表读取整份 payload。详情页的“采集快照”默认收起，选中历史版本时才读取对应 `seq`。

## 查询接口

[storage/dashboard.go](../backend/internal/storage/dashboard.go) 提供只读视图，调用方通过 `dashboardReader` 接口依赖所需能力：

| 方法 | 作用 |
|---|---|
| `Summary` | 概览统计 |
| `Catalog` | 内容/关注列表、搜索、排序、分页、问题筛选 |
| `Content` | 单个对象及其状态 |
| `Metrics` | 日指标序列 |
| `CollectionSeries` | 累计记录规模 |
| `Versions` | 内容动态与版本时间线 |
| `ReadSnapshot` | 按 `seq` 读取快照，`0` 表示最新 |

约束：

- `Catalog` 排序名走白名单；搜索转义 `%`、`_`、`\`；分页上限 48；`OnlyTargets` 才能包含未成功采集的目标。
- 每次查询都带时间上界，避免一次读取全部历史 payload。
- 版本对比只对照内容指纹投影中的字段（标题、简介、封面、发布时间、时长、作者归属、动态摘要、分P；作者为昵称、签名、头像、标识），忽略统计数和分P顺序。

## JSON 接口

`GET /api/metrics/{type}/{id}?days=7|30|90|all`：

```json
{"points":[{"at":1793836800000,"value":16271}],"truncated":false}
```

`days` 缺省为 30；非法范围返回 400。`GET /api/targets/status` 返回当前关注的 `Key/Name/State/Label/Reason/Last/Next`，供页面轮询“采集中”状态，不涉及上游请求。

## 迁移 0004

[0004_observation_status.sql](../backend/internal/storage/migrations/0004_observation_status.sql) 为每个关注目标保存一份最近采集状态，异常历史仍由查询日志承载：

- 新增列：`last_result`、`last_success_at`、`failure_count`、`last_failure_at`、`failure_reason`、`recovered_at`。
- 旧目标用已有成功观测回填 `last_success_at`；不改变内容快照、指纹和采样粒度。
- 新增索引：`idx_metrics_window`、`idx_video_versions_observed`、`idx_creator_versions_observed`。
- 失败累计与恢复在 `RecordTargetResult` 中一次覆盖：成功清零失败数，并从失败恢复时记录 `recovered_at`。

## 前端行为

- 样式与脚本内嵌在 [app.css](../backend/internal/admin/assets/app.css)、[app.js](../backend/internal/admin/assets/app.js)，无外部依赖。
- 趋势图使用内联 SVG；`segments` 在缺失日切断曲线，`summary` 对空数据和单点不计算增长，回落保留负值。
- 图表下方保留可展开的数据表；无脚本时表格仍可读。菜单、跳过导航、`focus-visible` 和 `prefers-reduced-motion` 已处理。
- 状态同时用文字和颜色表达；封面加载失败保留占位。

## 验证

- `go test ./...`：页面与 API、空库、输入规范化、停止关注保留历史、内容字段对比、看板查询、失败恢复、日志恢复。
- `node --test internal/admin/chart_test.cjs`：缺失日切断、单点不造增长、负增长。
- 手动检查：`/`、`/videos`、`/creators`、`/activity`、`/manage`、`/logs`、两类详情和两个 JSON 接口返回 200；空库、重复添加、失败目标、重启后状态恢复各一条路径。

## 边界

- 不新增登录、权限、多平台或前端构建流程。
- 内容页只读本地；作者视频发现只读，显式提交才经共用用例采集和关注。
- 不把 `last_run_at` 当最近成功时间；不使用内存日志推断对象状态。
- 不展示虚构曲线、全站排名、完整投稿数或未经确认的删除状态；作者视频发现范围、分页与限制见 [架构说明](architecture.md)。

作者视频选择、跨页添加全部、任务失败恢复和取消见 [作者视频设计](video-discovery.md)。
