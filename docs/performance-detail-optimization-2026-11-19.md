# 详情读取优化的本机验证与 VM 对照

日期为 2026-11-19（Asia/Shanghai）。本记录包含本机合成数据库、浏览器桩联调、公开平台只读样本和 VM 上的 32 并发接口对照。剩余验收项见 [实施任务](../PLANs/2026-11-19-client-detail-data-sources.md)。实现与协议见 [详情数据来源设计](client-detail-data-sources.md)。

## 功能和查询预算

- [真实SQLite驱动计数与计划测试](../backend/internal/storage/detail_sources_test.go)：`ReadDetailTarget` 一次查询、`Metrics` 一次查询，`ReadSavedContent` 额外一次；精确查询使用现有主键/索引，无当前版本读取进入正常历史路径，无新增schema迁移。
- [公共HTTP测试](../backend/internal/publicapi/detail_sources_test.go)：不存在、输入错误、版本与独立快照；读取不修改资料、不申请任务、不请求平台。
- [平台资料桩](../client/agent/internal/bilibili/metadata_test.go)、[协议验证](../client/agent/internal/remote/detail_sources_test.go)、[缓存与回退](../client/agent/internal/library/detail_sources_test.go)、[本地会话入口](../client/agent/internal/httpapi/detail_sources_test.go)：包括未知/零计数、身份不符、平台失败、服务器缺失或故障、旧缓存升级、切服与取关重关注、在途合并、取消和保存失败。
- [前端读取测试](../client/frontend/detail-sources.test.mjs)：缓存竞态、资料与曲线独立失败、时间范围仅刷新历史。新增模块也包含在内嵌资源及MIME回归中。
- Chrome 以1440、390像素视口加载真实HTML/CSS/ES模块，用本地Agent API桩验证初次不取版本/快照、范围仅刷新历史、游标前后翻页、选择版本只取一版、显式比较复用当前快照、保存资料来源、双端失败、迟到UP响应和图片预算。移动视口与文档宽度均为390，没有横向溢出。浏览器桩不代表真实B站或实际VM验收。
- [完整Agent浏览器测试](../client/agent/internal/httpapi/detail_browser_test.go) 通过实际本地会话、HTTP路由、独立状态文件、资料缓存与remote客户端，连接模拟平台及远端；验证当前资料、曲线、历史/比较、服务器回退、双端失败与未知记录状态。可显式提供Node/CDP驱动，或用 `BHT_DETAIL_BROWSER_URL_FILE` 外部驱动模式避开Windows测试进程对子浏览器的限制，普通单元测试默认跳过。

## 真实平台只读样本

使用本轮构建的Agent、独立临时数据目录、临时端口和新建本机会话，不配置服务器、不加载用户关注库、不提供Cookie，分别请求当前资料：

| 目标 | 当前资料可用 | 来源 | 有计数字段 | 服务器seq | 本次读取时间 |
|---|---|---|---|---:|---:|
| creator / `122541` | 是 | platform | 是 | 0 | 101ms |
| video / `BV17x411w7KC` | 是 | platform | 是 | 0 | 48ms |

这些只是单次公网连通性样本，不是延迟分位数、稳定性或平台全集验证。无法查询/已消失目标的回退用平台与服务器桩验证，不执行真实删除，也不推断失败必然代表删除。测试Agent已退出，产物位于被忽略的 `client/.cache/detail-live-results.json`。

## 合成本机HTTP处理器容量

[对照测试](../backend/internal/publicapi/detail_load_test.go) 显式设置 `BHT_DETAIL_LOAD_CHECK=1` 才运行，不增加普通单元测试负担。测试使用同一临时SQLite库、同一HTTP服务、单数据库连接，额外插入512个目标各30天的粉丝采样，共15360条，并保留一个可读取的作者及已有样本。`GOMAXPROCS=2`；每组发起窗口1秒，等待在途请求收尾后以实际总时长计算吞吐，所有组业务错误为0。

这里测处理器容量，单独绕过准入gate；生产路由仍保留全局限流。首次对照遇到10000 RPS gate，故将容量测试与生产准入分开，不能把下表当作默认限流配置可接纳的线上吞吐。未测平台查询和Agent节流/缓存，也没有本轮CPU或磁盘profile。Windows本轮时钟粒度导致部分亚毫秒样本显示0，按“小于约1ms”理解。

| 并发 | 旧完整详情 P50 / P95（ms） | 新基础历史 P50 / P95（ms） | 历史＋保存资料 P50 / P95（ms） |
|---:|---:|---:|---:|
| 1 | 11.01 / 12.01 | <1 / 1.00 | <1 / 1.00 |
| 8 | 83.48 / 135.00 | 1.00 / 2.00 | 1.01 / 3.00 |
| 16 | 164.12 / 278.53 | 1.01 / 4.00 | 3.00 / 6.00 |
| 32 | 334.44 / 539.44 | 3.00 / 8.00 | 6.00 / 12.99 |

“历史＋保存资料”表示两次连续HTTP请求，共3次SQL；不包含先尝试平台失败所用的时间，因此不是用户触发回退的总等待。

| 并发 | 旧详情操作/s | 新历史操作/s | 历史＋保存资料操作/s |
|---:|---:|---:|---:|
| 1 | 87.84 | 5687.17 | 3216.77 |
| 8 | 89.84 | 9503.90 | 5208.25 |
| 16 | 89.57 | 9552.19 | 5155.58 |
| 32 | 86.70 | 9389.49 | 5100.64 |

32并发下，累计连接等待分别为旧详情34983.53ms、新历史30336.69ms、历史加保存资料30222.57ms；对应操作数106、9414、5117。累计等待来自多个并发请求，不能直接比较总值或当作单次响应耗时；本轮平均每操作约330.03ms、3.22ms、5.91ms。SQL次数另由驱动计数测试证明，不能用WaitCount代替查询计数。

原始本机JSON可由环境变量 `BHT_DETAIL_LOAD_REPORT` 指定输出，当前运行产物位于被忽略的 `client/.cache/detail-load-results.json`。复现方式如下，Go工具链及临时目录按本机环境设置：

```powershell
cd backend
$env:GOMAXPROCS='2'
$env:BHT_DETAIL_LOAD_CHECK='1'
go test ./internal/publicapi -run '^TestDetailLoadComparison$' -count=1 -v
```

上表用于比较本机合成数据上的查询路径。实际 VM 延迟见下面独立测量。

## VM 32 并发接口对照

北京时间 15:40 至 15:45，Windows 压测客户端通过 HTTP/1.1 连接同一个 VM 服务进程。三组使用同一批 91 个已记录 UP、最近 30 天的采样范围和容量模式，按顺序各运行 60 秒。表中吞吐按窗口内成功的业务操作计算，延迟取成功操作的分位数。

| 场景 | 每操作 HTTP 请求数 | SQL 次数 | 成功操作/秒 | P50（ms） | P95（ms） | P99（ms） |
|---|---:|---:|---:|---:|---:|---:|
| 旧完整详情 `detail` | 1 | 8 | 30.12 | 1008.80 | 1730.58 | 2072.60 |
| 轻量历史 `history` | 1 | 2 | 4647.65 | 6.50 | 11.55 | 14.70 |
| 轻量历史加最后保存资料 `history-saved` | 2 | 3 | 2300.42 | 13.31 | 21.31 | 26.23 |

三组已发出的请求均成功。回退组有 23 次操作在历史请求完成后到达测量截止时间，脚本没有发出对应的保存资料请求。回退组测量两次顺序 HTTP 读取，用户打开页面时的耗时还取决于 Agent 缓存和平台尝试。原 [2026-11-18 测量](performance-analysis-2026-11-18.md) 的旧路径 P50 为 626.17 和 771.16 ms，本轮以同一进程内测得的 1008.80 ms 做对照。其余并发档位、CPU、磁盘、连接等待和 Agent 整页时延仍待验证。

原始 JSON 位于本机被 Git 忽略的 `backend/bin/vm-detail-results/`，包括 `detail.json`、`history.json`、`history-saved.json` 和 `targets.json`。另一次旧详情的终端记录为 31.30 次/秒、P50 968.65 ms，该轮文件随后被第二次旧详情结果覆盖。
