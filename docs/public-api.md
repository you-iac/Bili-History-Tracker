# 公共接口与管理后台端口

## 应答协议更新（2026-11-17，已实现）

正常业务统一HTTP200，JSON以ok/code和目标状态区分。完整约定见 [公共JSON应答](public-response.md)，实施与验证见 [计划](../PLANs/archive/2026-11-17-public-json-response.md)；请求错误、内部故障与管理端边界保留。

状态：已实现并通过自动验证。设计文档与[实施计划](../PLANs/archive/2026-11-16-public-api-routing.md)先以`abf5847`提交，随后完成代码；用户已确认提交实现，未推送。本文记录当前协议，远端部署与用户体验尚未进行。

## 职责与调用

`publicapi` 负责 8181 路由、JSON 协议、入口限流及批量截断；`httpapi` 保留实时平台查询，`storage` 提供公共历史读取，`tracker.RequestTarget` 验证并接纳跟踪。`admin` 只负责 8182 本地管理页面及其内部交互，不作为 Agent 入口。

```text
+--------------------------+     HTTP(S)     +--------------------------+
| Agent / API caller       | --------------> | publicapi :8181          |
| public reads / targets   |                 | routes / rate / batch    |
+--------------------------+                 +--------------------------+
                                                        |
                                      read / accept / live query
                                                        v
                                            +--------------------------+
                                            | storage / tracker        |
                                            | httpapi / source         |
                                            +--------------------------+
                                                        ^
                                             management read / command
                                                        |
+--------------------------+   local / SSH   +--------------------------+
| administrator browser    | --------------> | admin :8182              |
+--------------------------+                 | HTML / private UI actions|
                                            +--------------------------+
```

箭头表示运行时请求，结果沿原链路返回；两端使用同一存储和采集实例。Cookie、私有关注文件、媒体和评论正文不通过公共接口上传。提交的 ID 和查询参数仍对服务器可见。

## 8181 路由

所有客户端路由直接返回 JSON，不再依赖 `Accept` 在 HTML 与 JSON 之间切换。

| 方法与路径 | 数据与行为 |
|---|---|
| `GET /healthz` | 健康状态，不请求上游；同样经过入口限流 |
| `GET /` | 公共统计及近期内容，不包含管理日志、凭据或私人数据 |
| `GET /creators`、`GET /videos` | 数据库目录；保留 `q/id/owner/sort/page` 参数及 `items/page/limit/total` 响应 |
| `GET /detail/{type}/{id}` | 数据库详情、`days=7/30/90/all` 指标、版本分页及可选 `seq` 快照 |
| `GET /api/detail/{type}/{id}?days=7或30或90或all` | 轻量服务器 `record/target/metrics`；不返回当前快照、版本列表或总数，2次SQL |
| `GET /api/detail/{type}/{id}/saved` | 最后保存的归一化 `item/description/snapshot_at`，seq与资料同次关联，1次SQL；供平台读取失败后回退 |
| `GET /api/detail/{type}/{id}/versions?before=正序号` | 递减seq的十条游标页；`items/has_more/next_before`，无总数，1次SQL |
| `GET /api/detail/{type}/{id}/snapshot?seq=正序号` | 只读指定版本 `kind/id/seq/at/snapshot`，1次SQL |
| `GET /api/targets/status?kind=...&id=...` | 精确返回 `kind/id/recorded/tracking/status`；未知目标不等于下架 |
| `GET /api/metrics/{type}/{id}?days=...` | 已有成功资料的目标指标历史；作者占位行返回 `DETAIL_NOT_RECORDED` 与真实任务状态，完整资料无样本时成功返回空数组；沿用真实 UTC 日采样口径 |
| `POST /api/metrics/batch` | 每页最多24个不同ID的7日采样；仅 fallback_ids 指定的本地失败目标附带轻量资料兜底 |
| `GET /api/history/{type}/{id}` | 历史版本分页与 days 指标；指定 seq 时只读取该快照，不读取当前资料或任务 |
| `GET /api/metadata/{type}/{id}` | 本地无法读取当前资料时的单目标已有资料兜底，不请求上游 |
| `POST /targets` | 单目标或有上限的批量申请，只接受 JSON，不接受后台表单或采集间隔 |
| `GET /api/v1/platforms/{platform}/videos/{id}` | 保留原有实时上游查询及 `{data:...}` 应答，不写跟踪目标 |
| `GET /api/v1/platforms/{platform}/creators/{id}` | 同上；实时查询也消耗统一入口额度 |

8181 不提供 `/manage`、`/logs`、`/assets`、`/targets/delete`、`/targets/run`、`/targets/interval`、作者视频后台发现或导入任务接口。

详情按目标读取一次摘要，再读取指标和版本；目标启停与运行状态另行确认。单次请求的观测字段来自该摘要，不提供跨 SQL 的事务快照，不跨请求缓存。接口字段与业务码保持不变，实测范围和列表剩余成本见 [详情查询性能](detail-query-performance.md)。

新 Agent 使用本地当前资料与历史专用入口；公开目录带 `projection=identity` 时只返回已记录目标身份，普通分页不执行通用目录聚合。协议、边界、批量 JSON 示例与本地投稿口径见 [本地优先资料](local-first-metadata.md)。原目录与详情入口保留给旧调用方。

## 请求限制

| 配置 | 默认 | 范围与含义 |
|---|---|---|
| `BHT_ADDR` | `0.0.0.0:8181` | 公共接口默认监听所有 IPv4 网卡，客户端连接服务器实际 IP；可显式覆盖地址和端口 |
| `BHT_ADMIN_ADDR` | `127.0.0.1:8182` | 管理监听地址；保持本机访问或使用 SSH 隧道 |
| `BHT_API_RPS` | `20` | 正整数，最大 10000；进程共享，任意滚动一秒接纳次数及在途 HTTP 请求数均不超过此值 |
| `BHT_API_BATCH_MAX` | `10` | 正整数，最大 100；一个批量 JSON 只处理前 N 个原始数组位置 |

配置缺省时使用默认值；非数字、零、负数或超出范围使服务启动失败，避免误配置后不受限。限额涵盖不同路径、不同 IP、原有实时 API 及健康检查。达到次数或在途上限返回 HTTP200、`ok:false/code:RATE_LIMITED/retry_after_seconds:1`，附 `Retry-After: 1`，不等待空位。拒绝的请求不进入业务处理。仅有一个流量参数，不增加账号、令牌、每 IP 表或持久化队列。

JSON 请求体上限 64 KiB，超出返回 413；格式错误、未知字段或混合单目标和批量字段返回 400；不是 `application/json` 返回 415。已有来源检查保留用于拒绝浏览器跨站操作，但它不是身份认证。

这些限制不能替代长期跟踪目标总预算、操作系统资源管理或公网带宽防护；本次按用户要求不增加访问令牌或复杂准入。

## 跟踪提交与应答

单目标提交同步返回业务应答；HTTP200仍须检查ok/code：

```json
{"type":"creator","id":"72541187"}
```

```json
{"ok":true,"code":"OK","kind":"creator","id":"72541187","tracking_state":"active","tracking":true,"recorded":true,"status":"ok","result":"created"}
```

平台由当前服务器配置固定为 B站。UP 主为正整数 UID，视频为 BV 号。标准链接只提取 ID，不作为任意网址代理。新目标先验证并保存，已有任务复用原采集间隔和资料。

单目标和批量中的每一项都以提交后的状态读取确认跟踪，`active` 时返回 `result:created` 或 `result:existing`。读到暂停返回 `TARGET_PAUSED`，读到任务已移除返回 `TARGET_SUBMISSION_FAILED`，并附读到的状态。无法验证、并发繁忙、容量限制和处理超时也使用 HTTP 200 及 `ok:false`，业务码见 [应答协议](public-response.md)。本服务状态读取或持久化故障使用 HTTP 500。失败或断连后先查精确状态，再按有界退避重试。

批量使用同一路径，字段为 `targets`，不改变现有 Agent 的单目标调用：

```json
{"targets":[{"type":"creator","id":"72541187"},{"type":"video","id":"BV17x411w7KC"}]}
```

响应包含 `received`（原始项数）、`processed`（实际产生结果的项数）、`ignored`（超过 N 的项数）、`results`（原始顺序的逐项结果）。每项包含 `index/kind/id/ok/code/result/target/tracking/status`，`http_status`保留为旧诊断字段，失败项额外包含 `error`。批量本身格式有效时返回 200，是否成功只看逐项应答，不能据此确认被忽略的尾部。

前 N 项中的无效项或重复项仍占一个位置，不跳过后继续补足 N 个成功目标；忽略项不做目标校验、不请求上游、不保存、不排队。整个批次按顺序处理且共享25秒接纳预算，已取得有效资料的当前项目允许独立有界保存、建任务及确认，保守业务上限约36秒。预算耗尽后尚未开始的前 N 项合法目标返回 `ok:false/code:REQUEST_TIMEOUT/stage:check/outcome:not_applied`，旧诊断字段为504，不继续采集；结果未知时不返回默认 `tracking:false`。每项保留 `stage/outcome` 和真实确认状态，失败后先查状态，不删除此前成功提交的数据。具体阶段预算与单目标语义见 [分阶段申请设计](request-stages.md)。正确 JSON 和 64 KiB 限制适用于整个请求，不能利用尾部绕过语法或大小校验。

## 8182 与地址迁移

8182 页面只提供管理 HTML，取消客户端的同路径 JSON 输出；`POST /targets` 只接受管理表单。原 `/api/*` 不保留别名，不作到 8181 的自动重定向。管理页面自身的图表、状态轮询及作者视频发现/导入请求改为 `/manage/metrics/...`、`/manage/targets/status`、`/manage/creators/...`、`/manage/ingest/...`，这些是后台内部交互，不向 Agent 或公共端口提供。

更新 backend 后，在 Agent 设置中将服务器基地址改为实际的 8181 地址。公共入口默认可通过服务器 IP 直连；SSH 隧道也可继续使用本机 `18181` 转发服务器 `127.0.0.1:8181`，管理隧道单独使用 `18182` 转发 `127.0.0.1:8182`。已有自定义地址不自动改写，包括 systemd 中显式配置的 `BHT_ADDR=127.0.0.1:8181`；要采用新的默认监听范围，需要移除该覆盖或改为 `0.0.0.0:8181` 并重启服务。公网部署可由 HTTPS 反向代理转发本机 8181，需要仅本机监听时显式设置 `BHT_ADDR=127.0.0.1:8181`，8182 默认保持本机监听。

虚拟机直接运行的示例（在数据库所在目录，使用测试副本）：

```sh
BHT_DB="$PWD/bht.db" BHT_ADDR="0.0.0.0:8181" BHT_ADMIN_ADDR="127.0.0.1:8182" ./server
```

限额可另外设置`BHT_API_RPS=20 BHT_API_BATCH_MAX=10`；省略即为这些默认值。客户端连接虚拟机IP的8181，管理后台使用虚拟机本机浏览器或单独的SSH隧道。此示例不负责公网TLS或防火墙配置。

## 验证记录

- 两个Go模块的`go test ./...`、`go vet ./...`和Windows构建通过，另生成Linux amd64服务器程序；未操作真实数据库。
- 公共入口测试覆盖读请求无上游调用、旧实时API应答、跨接口共用限额、滚动窗口/并发竞争/慢请求/异常释放、批量截断/无效项占位/重复/暂停/失败/取消及64 KiB边界。
- 两个真实本机HTTP监听验证共享数据：公共申请后后台可读，后台管理请求不出现在公共端口，两端都不能借`/targets`进入另一种权限分支。
- 客户端`pnpm lint/test`（16项）及后台Node测试（6项）通过。Edge使用真实服务器与真实Agent、隔离数据目录，验证公共连接成功、误连管理端口提示、后台页面、内部状态读取及40并发请求恰有20成功/20返回带Retry-After的429；无页面脚本错误。
- 本地联调使用空库或平台桩，没有真实Cookie或B站网络采集，不代表已部署虚拟机或公网服务器。
