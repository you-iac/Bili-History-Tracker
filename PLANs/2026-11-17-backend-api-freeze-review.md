# 后端 API 固定前审查问题与修复计划

- 创建时间：2026-11-17 14:51（Asia/Shanghai）。
- 状态：部分实施。2026-11-18 完成 BACK-TARGET-REPLY，并补齐 BACK-SHUTDOWN 的 SIGTERM 停机入口。其余问题与后台退出验收仍待完成。下方保留原审查现场。
- 审查基线：提交 `1baa4ab`（统一业务 JSON 应答并修复 Agent 跟踪核对）；此前设计提交为 `7fab9e9`。后续实施须先确认基线是否变化。
- 来源：用户要求最后检查 backend 设计，计划部署后固定 API、尽量减少后端改动；审查后要求将五项发现详细记录到 PLANs。
- 访问控制约定：用户明确选择服务器防火墙／IP 白名单，本计划不增加 Token、账号或认证接口。8181 仅向允许的来源开放，8182 保持本机管理访问。
- 关联：[JSON 应答协议](../docs/public-response.md)、[公共 API](../docs/public-api.md)、[上一轮实施记录](archive/2026-11-17-public-json-response.md)、[架构导航](../docs/architecture.md)。

## 审查结果与证据边界

审查时工作区干净，原有 backend 的 `go test -p 1 ./...` 和 `go vet -p 1 ./...` 通过。额外构造的四个临时回归用例暴露了原有测试未覆盖的边界。原有套件通过不代表下面四种输入／并发现场已经通过。

| 问题标识 | 优先级 | 发现 | 证据状态 |
|---|---|---|---|
| BACK-ADMIN-ORIGIN | P1 | 管理表单接受跨站写入 | 隔离数据库临时测试已复现 |
| BACK-TARGET-REPLY | P2 | 提交成功字段与当前跟踪状态矛盾 | 已修复，可控时序的单目标和批量回归通过 |
| BACK-TIMEOUT-CODE | P2 | 落库阶段取消被归为内部故障 | 临时测试直接调用 submit 已复现；HTTP 映射由代码确认 |
| BACK-PROFILE-METRICS | P2 | UP 占位资料在详情与指标接口中判定不一致 | 临时 HTTP 处理器测试已复现 |
| BACK-SHUTDOWN | P2 | Linux SIGTERM 和后台任务退出链路不完整 | 静态审查确认；尚未完成 Linux 信号实测 |

四个临时用例通过 Go overlay 注入，未修改受 Git 管理的源码；数据库由 `t.TempDir()` 创建，上游使用测试桩。没有调用真实 B站、使用真实 Cookie，或修改远程服务器／用户数据库。管理请求使用 `httptest` 验证服务端行为，不等同于已经验证所有浏览器都能完成跨站攻击。

本机临时材料位于 `client/.cache/backend-final-review/`，属于忽略目录，可能被清理，不能作为长期验收依赖；下面已经保留必要的触发步骤和关键输出。正式修复时应把对应场景加入仓库内的持久测试。

## BACK-ADMIN-ORIGIN：管理表单缺少跨站写入保护

### 影响与出现条件

8182 管理页面的添加、删除、立即采集和调整间隔属于有副作用的表单操作。当前只把管理端口放在本机并不能替代 HTTP 请求来源校验。

当管理服务可由某个浏览器访问时，例如直接访问本机服务，或经 SSH 本地端口转发管理远程服务，跨源表单请求若到达服务端，现有处理器仍会执行。防火墙／IP 白名单决定网络来源是否允许，不能识别同一允许来源上的页面意图。

已经证明的是“服务端接受带跨站标记的管理写入”。未进行真实浏览器攻击验证，也未验证不同浏览器的私网访问限制；不能把临时测试表述成任意外部网页均可稳定利用。

### 复现现场

1. 用临时 SQLite 创建管理服务，插入 creator `123` 的跟踪任务。
2. 向管理处理器提交如下请求；上游为不会发真实请求的测试桩。

```http
POST /targets/delete HTTP/1.1
Host: 127.0.0.1:8182
Origin: https://untrusted.example
Sec-Fetch-Site: cross-site
Content-Type: application/x-www-form-urlencoded

type=creator&id=123
```

3. 读取响应并查询该临时跟踪任务是否仍存在。

实际日志关键字段：

```text
TestReviewAdminCrossSiteFormCanDeleteTarget
cross-site form: HTTP 303, target still exists=false
FAIL: cross-site management write was accepted
```

预期应拒绝请求，且任务、间隔、历史及采集次数不变。当前返回重定向并删除了任务；已保存的内容历史并未因此删除。

### 原因与代码位置

- [admin/server.go](../backend/internal/admin/server.go) 的 `Server.Handler` 直接注册表单处理器，缺少覆盖这些写操作的统一来源保护。
- 同文件 `deleteTarget` 从 `r.FormValue` 读参数后直接调用 `storage.RemoveTarget`；`addTarget/runTarget/setInterval` 也承担写入或启动任务的职责。
- [admin/discovery.go](../backend/internal/admin/discovery.go) 的 `sameOriginJSON` 只保护相应 JSON 路径，不覆盖上述表单。不能因 JSON 路径已做来源校验，就认定整个后台都已保护。

### 建议修复

1. 在管理入口为所有有副作用的操作建立统一来源保护，兼容表单和现有 JSON，不只修复删除接口。
2. 对明确的 `Sec-Fetch-Site: cross-site` 拒绝；解析并校验非空 `Origin` 的 scheme、host、port 是否属于配置认可的管理来源。
3. 明确无 Origin 的本机工具请求如何兼容，并为该策略写测试；不把可由客户端填写的来源头当作身份认证。若增加 CSRF token，须覆盖表单生成及验证全链路，不留下仅部分路径受保护的状态。
4. 拒绝应发生在解析业务命令、写数据库和启动 goroutine 之前。管理端保持原有 HTTP 错误语义，不套用8181的“正常业务200”规则。
5. 保持防火墙白名单与8182本机监听约束，不为此增加公共 Token／账号系统，也不要求改公共 API 路径。

### 验收条件

- 上述跨站删除请求被拒绝，creator `123` 的任务仍存在。
- 添加、删除、立即采集、调整间隔及管理 JSON 写操作均覆盖同源允许、跨源拒绝、非法 Origin、缺少 Origin 的明确策略。
- 被拒绝请求不得产生平台查询、后台任务或数据库副作用。
- 正常管理表单、SSH 转发访问方式与重定向流程仍可使用。
- 回归入口建议：[admin/admin_test.go](../backend/internal/admin/admin_test.go)、[admin/isolation_test.go](../backend/internal/admin/isolation_test.go) 及相关管理 JSON 测试。

## BACK-TARGET-REPLY：提交成功应答可能与任务状态矛盾

### 实施记录（2026-11-18 09:06，Asia/Shanghai）

[提交实现](../backend/internal/publicapi/targets.go) 用最终读取的状态生成单目标和批量逐项应答。确认 `active` 才报告成功，已暂停返回 `TARGET_PAUSED`，已移除返回 `TARGET_SUBMISSION_FAILED`，两者都附实际读到的目标状态。状态读取失败保留 `INTERNAL_ERROR`。应答反映本次读取结果，采集间隔和任务启停仍由原有流程管理。协议细节见 [公共 JSON 应答](../docs/public-response.md#目标与接口)。

[回归测试](../backend/internal/publicapi/targets_test.go) 在提交之后、最后一次查询之前确定性地修改临时 SQLite，覆盖新建及复用任务时的暂停、删除和查询失败，以及启用但未记录资料的应答。旧逻辑复现误报，修复后回归连续三次通过。后端全量 Go 测试、`go vet`、Windows 服务构建、Agent 全量 Go 测试以及客户端 `pnpm lint`、`pnpm test` 均通过。以下为修复前的审查现场。

### 影响与出现条件

提交复用已有任务时，管理员或其他数据库写入者可能在“RequestTarget 已检查到 active”和“公共层再次读取 target 状态”之间暂停／移除任务。当前响应仍可能确认成功并声明 `tracking:true`，同时又返回 paused／absent 状态。

Agent 对新协议会检查这些字段是否一致，因此可能将应答视为协议错误并进入重试，而非识别暂停或状态变化。批量调用方若只检查顶层成功标记，也可能得到错误的跟踪确认。

### 复现现场

1. 在临时库中创建已有 creator `12345` 的资料和跟踪任务。
2. 一个 goroutine 对该临时任务交替写入 `enabled=0/1`，模拟并发启停，并在写入间让出调度。
3. 同时循环调用 `publicapi.Server.submit`，最多400次，检查操作成功时 `result.Tracking` 是否等于 `result.Target.Tracking`。
4. 实际运行在第一次调用即观察到矛盾。摘录关键字段，省略临时指针地址：

```text
TestReviewSuccessfulReplyCanContradictCurrentTrackingState
OK:true Code:OK Result:existing HTTPStatus:200 Tracking:true Status:paused
Target: TrackingState:paused Tracking:false Recorded:true Status:paused
FAIL: contradictory successful reply
```

临时测试直接复现的是 `submit` 返回对象。根据当前单目标路由的序列化代码，该对象会进一步生成相当于下面的 HTTP200 JSON；此示例为代码推导，不是远程抓包：

```json
{
  "ok": true,
  "code": "OK",
  "kind": "creator",
  "id": "12345",
  "tracking": true,
  "tracking_state": "paused",
  "recorded": true,
  "status": "paused",
  "result": "existing"
}
```

并发删除任务也存在相同检查窗口，但此次动态复现采用的是启停切换，不将未实测的删除交错写成已复现事实。

### 原因与代码位置

- [tracker/request.go](../backend/internal/tracker/request.go) 的 `Tracker.RequestTarget` 检查既有任务后返回 `existing`。
- [publicapi/targets.go](../backend/internal/publicapi/targets.go) 的 `Server.submit` 随后调用 `targetStatus`，却无条件设置 `OK=true`、`Tracking=true`。
- 同文件 `requestTarget` 的单目标成功 JSON 再次硬编码 `tracking:true`，而 `tracking_state/recorded/status` 来自后一次读取。
- [publicapi/response.go](../backend/internal/publicapi/response.go) 的 `targetStatus` 可以正确返回 paused；问题在于调用方没有根据这个结果重新判断能否确认跟踪。

### 建议修复

1. 成功与失败判定，以及平铺／嵌套的目标字段，都从最终确认的目标状态推导，不能分别硬编码。
2. 最后读取为 active 时才能确认当前已跟踪；paused 返回现有 `TARGET_PAUSED` 业务结果并附真实 target，不自动恢复。
3. 最后读取为 absent 时，不再返回已确认成功。优先复用 `TARGET_SUBMISSION_FAILED` 并说明状态已变化、须核对；若决定新增专用业务码，必须在接口固定前补齐协议及 Agent 兼容。
4. 单目标和批量共用相同的结果构建逻辑，消除“某一条路径改对、另一条仍硬编码”的情况。
5. 不跨上游网络调用持有数据库长事务或全局锁。允许响应完成后任务再发生变化；本修复保证一次应答内部一致，不承诺永久跟踪状态。

### 验收条件

- 对 active、paused、absent 的最终状态，`ok/code/tracking/tracking_state/target` 内部一致。
- 覆盖复用／首次创建期间并发暂停、删除及状态查询失败；单项与批量均不得假报确认成功。
- active 但无资料仍表达 `tracking:true,recorded:false`，不触发重复立即采集。
- 暂停不被申请自动恢复，已有任务的采集间隔不被改变。
- 将概率性启停复现补成可控交错的稳定测试，或增加可重复的并发压力回归，避免只依赖“这次循环没遇到”。
- 回归入口建议：[publicapi/targets_test.go](../backend/internal/publicapi/targets_test.go)、[publicapi/response_test.go](../backend/internal/publicapi/response_test.go)、[tracker/request_test.go](../backend/internal/tracker/request_test.go)。

## BACK-TIMEOUT-CODE：落库阶段取消／超时被误报为内部故障

### 影响与出现条件

上游查询已经返回有效资料，但请求在开始数据库事务或落库过程中被取消／耗尽时间预算。该错误同时具有“存储调用失败”和“context 取消／超时”的性质；当前分支顺序先把它作为服务器内部故障处理。

这会破坏正常业务超时使用 HTTP200、`REQUEST_TIMEOUT` 的约定，并使日志／调用方错误地把请求生命周期结束理解为数据库故障。结果未知时 Agent 应先核对，而不能依据此类错误推断内容失效。

### 复现现场

临时用例的关键执行顺序：

```text
ctx = context.WithCancel(...)
creator 查询桩：
  先生成有效 CreatorProfile
  cancel()
  返回 profile, nil
submit(ctx, creator 1)
  -> SaveCreator 使用已取消 context
  -> 包装为 ErrTargetStorage
  -> 命中 INTERNAL_ERROR
```

实际输出：

```text
TestReviewCancelledPersistenceIsReportedAsInternalError
cancelled after upstream returned: diagnostic=500 code=INTERNAL_ERROR ok=false
FAIL: request cancellation was classified as INTERNAL_ERROR
```

此用例直接调用的是 `submit`，观察到诊断字段 `HTTPStatus=500`。单目标路由会据此选择实际 HTTP500。尚未通过真实网络等待满20秒复现 deadline 场景；deadline 的同类风险由相同包装／分支顺序推导。

真实客户端如果已经断开，不能保证它还能收到任何 JSON；验收应分别检查可回复的服务器超时与已经取消的请求，不把两者混为一谈。

### 原因与代码位置

- [tracker/request.go](../backend/internal/tracker/request.go) 中数据库失败通过 `errors.Join(ErrTargetStorage, err)` 保留错误链；视频保存链路也可能经 [tracker/ingest.go](../backend/internal/tracker/ingest.go) 返回该标记。
- [publicapi/targets.go](../backend/internal/publicapi/targets.go) 的 `Server.submit` 先匹配 `ErrTargetStorage`，后匹配 `context.DeadlineExceeded/context.Canceled`。
- 包装本身保留了取消原因，但错误映射优先级使后面的超时分支无法处理该现场。

### 建议修复

1. 先识别错误链中的取消／deadline 原因，再判断真正的存储内部故障；不解析错误文字。
2. 如某个驱动返回值没有保留 context 原因，应明确何时可依据当前 context 做补充判断，避免仅因响应时 context 恰好过期就掩盖不相关的真实数据库错误。
3. 对仍可回复的业务超时返回 HTTP200、`ok:false/code:REQUEST_TIMEOUT`；真实数据库损坏、关闭或其他内部故障继续500。
4. 取消／超时不能伪造 `tracking:false` 表示已查明无任务；无法确认时保持结果未知，后续先查精确状态。
5. 保留可能已经提交成功的事务及资料；不尝试用删除记录“回滚”已经提交的业务，也不因超时重复要求立即采集。

### 验收条件

- 上游返回前超时、上游成功后落库前取消、事务中超时／取消、提交后应答超时均有覆盖。
- 特别覆盖同时满足 `ErrTargetStorage` 与 context 错误链的情况。
- 数据库关闭或真实存储故障仍返回500、`INTERNAL_ERROR`。
- 已经接受的任务能通过状态查询确认，重试不产生额外任务或修改既有间隔。
- 回归入口建议：[publicapi/targets_test.go](../backend/internal/publicapi/targets_test.go)、[publicapi/response_test.go](../backend/internal/publicapi/response_test.go)。

## BACK-PROFILE-METRICS：UP 占位资料的详情和指标判定不一致

### 影响与出现条件

保存视频时，存储层会建立该视频作者的引用行；这不等于已经成功采集作者完整资料。此时 creator 行存在，但没有完整资料版本，通常表现为 `seq=0`。

同一个仅有引用的 UP，目前详情能正确报告无资料，指标接口却按“Content 查询成功”认定存在成功资料，返回 `ok:true/code:OK` 和空数组，调用方难以区分“无完整资料”与“有资料但暂无指标样本”。

### 复现现场

1. 向临时库保存视频 `BV17x411w7KC`，作者为 creator `987`。
2. 不调用 `SaveCreator`，因此仅生成作者引用／占位行。
3. 对同一处理器分别读取详情和30日指标，均不触发上游采集。

实测关键响应：

```text
GET /detail/creator/987
HTTP 200
ok=false, code=DETAIL_NOT_RECORDED
target: tracking_state=absent, tracking=false, recorded=false, status=profile_pending

GET /api/metrics/creator/987?days=30
HTTP 200
{"code":"OK","ok":true,"points":[],"truncated":false}

FAIL: placeholder profile treated as successful record
```

这不是“空数组一律应报错”。真正有成功资料但未提供粉丝指标／尚无窗口内样本时，空指标数组可以是正常结果；缺少的是对“成功资料是否存在”的统一判断。

### 原因与代码位置

- [storage/repo.go](../backend/internal/storage/repo.go) 的 `Store.SaveVideo` 会通过作者引用写入建立占位信息。
- [publicapi/read.go](../backend/internal/publicapi/read.go) 的 `clientDetail` 除 `Content` 错误外，还检查 `!i.Recorded` 以及 creator `Seq==0`。
- 同文件 `metrics` 只检查 `Content` 是否返回错误，丢弃成功返回的内容对象，没有做完整资料判定。
- [publicapi/response.go](../backend/internal/publicapi/response.go) 的 `targetStatus` 已经将 creator `Seq>0` 作为 recorded 的必要条件，三个入口没有复用一致规则。

### 建议修复

1. 提取或复用“存在成功观测资料”的统一判断，用于详情、指标和目标状态，不只判断数据库行是否存在。
2. UP 占位资料返回 HTTP200、`DETAIL_NOT_RECORDED` 和真实 target；有任务但无资料时仍为 active，不误判成无任务。
3. 真正已有完整资料而无指标样本时，继续返回 `OK` 与空 points，不补造零粉丝／零播放。
4. 保持所有读取只读；无资料不能在指标接口中隐式触发上游采集或创建跟踪。
5. 将 creator 引用与完整资料的区别写入接口契约和对应代码注释，不要求新增数据库字段或迁移。

### 验收条件

- 覆盖未知作者、仅视频引用作者、占位作者已有 active 任务、paused 任务、已有完整资料但无指标及真实零指标。
- 详情／指标／状态查询的 recorded 语义一致。
- 空样本与无成功资料明确区分，范围7/30/90/all仍按原口径读取。
- 不删除视频关联引用、作者历史和指标，不引入上游副作用。
- 回归入口建议：[publicapi/read_test.go](../backend/internal/publicapi/read_test.go)、[publicapi/response_test.go](../backend/internal/publicapi/response_test.go)、[storage/creator_test.go](../backend/internal/storage/creator_test.go)。

## BACK-SHUTDOWN：Linux SIGTERM 与后台退出链路不完整

### 实施记录（2026-11-18 08:43，Asia/Shanghai）

按用户要求先补齐信号入口：[main.go](../backend/cmd/server/main.go) 在保留 `os.Interrupt` 的同时注册 `syscall.SIGTERM`，两者共用现有停机流程。新增 [shutdown_linux_test.go](../backend/cmd/server/shutdown_linux_test.go)，在临时数据库、日志和随机端口下，以子进程验证空闲服务收到真实 SIGINT／SIGTERM 后进入现有关闭路径并正常返回；不调用真实上游。后台任务等待、关闭预算及日志真实性等原问题仍未修复，本项保持未完成。以下内容为原审查现场，不代表补齐入口后的代码。

本次验证：Windows/amd64 的 `go test -p 1 ./...`、`go vet -p 1 ./...` 和服务构建通过；`GOOS=linux GOARCH=amd64 CGO_ENABLED=0` 的服务构建、`go test -c ./cmd/server` 测试二进制交叉编译及 `go vet -p 1 ./cmd/server` 通过。当前环境 WSL 枚举返回 `E_ACCESSDENIED`，未运行 Linux 信号测试，交叉编译结果不替代真实目标平台运行验收。架构和入口文档已同步说明信号及现有退出边界。

### 影响与出现条件

Linux 服务停止／重启、容器退出等常见部署流程会发送 SIGTERM。当前服务仅注册 `os.Interrupt`，没有把 SIGTERM 纳入主动停机流程。具体部署是否使用该信号，应在真实服务管理配置中确认。

即便进入现有停机路径，代码也没有显式等待调度 goroutine 结束，异步“立即采集”任务还使用独立 background context。数据库／日志关闭可能早于后台收尾。此问题不等于已经证明数据库损坏；SQLite WAL 能处理许多非正常退出场景，但不能据此宣称应用停机链路已正确执行。

### 静态现场与证据限制

[cmd/server/main.go](../backend/cmd/server/main.go) 当前执行链路：

```text
signal.NotifyContext(..., os.Interrupt)
  -> go scheduler.Run(ctx)
  -> 等待 ctx 或监听错误
  -> apiServer.Shutdown(共享5秒context)，忽略返回错误
  -> adminServer.Shutdown(同一context)，忽略返回错误
  -> 输出 "database closed, scheduler stopped"
  -> 返回后由 defer 关闭管理任务、日志、数据库
```

补充实现事实：

- SIGTERM 未注册，不能保证 Linux 常见停止方式走到上述收尾代码。
- `go scheduler.Run(ctx)` 没有对应 done channel／WaitGroup 等待。
- [tracker/tracker.go](../backend/internal/tracker/tracker.go) 的 `Tracker.RunNow` 使用 `context.Background()` 启动异步工作，不随主 context 自动取消。
- `recordOutcome` 自带最多5秒的 background 收尾，可能晚于原目标 context 结束。
- 两个 HTTP Shutdown 顺序共用同一个5秒预算；第一个耗尽后第二个得不到完整等待时间，且错误被忽略。
- “database closed, scheduler stopped”日志在真正执行 defer 关闭前就输出，并未验证调度器已退出。

本项来自静态审查；上一轮只做了 Windows 构建，没有记录真实 Linux SIGTERM、长请求或长期调度任务的停机实测结果。

### 建议修复

1. 注册 SIGTERM 并保留 SIGINT／os.Interrupt 支持，保持 Windows 和 Linux 均可构建。
2. 明确统一的停机预算与阶段：停止接收新任务、通知取消／允许有界收尾、等待 HTTP 和后台工作、最后关闭日志与数据库。
3. 为 Scheduler.Run 增加明确的完成等待，不能只 cancel 后立即关闭依赖。
4. 将 RunNow 等异步任务纳入生命周期管理，保证停止接收与任务计数／等待之间没有新增任务竞态；不能只等待调度器而遗漏手动采集。
5. 明确现有 HTTP 请求 context 的取消策略；Shutdown 本身不等于取消所有后台工作。对超过预算的请求／任务定义终止和日志行为。
6. 处理两个 HTTP Shutdown 的错误，采用并行关闭或明确定义的总预算；不要无条件输出“已关闭／已停止”。
7. 仅在对应阶段实际完成后输出完成日志。对无法在预算内结束的情况保留真实错误，不将正常停机推断为内容下架。
8. 与部署端停止宽限时间配合；不得在未确认服务器架构和管理方式前宣称 Linux 部署已验收。

### 验收条件

- 在临时目录、临时数据库和模拟上游下，验证空闲／有活动 HTTP 请求／调度采集／RunNow 工作时的 SIGINT 与 SIGTERM。
- 证明信号触发主动退出，停止接收新任务，并在关闭数据库和日志之前完成或有界终止后台工作。
- 可用 channel／可控阻塞查询建立稳定生命周期测试，避免仅通过固定 sleep 猜测退出顺序。
- 验证某个 HTTP Shutdown 超时不会被当作所有服务均正常结束；日志与实际结果一致。
- 两平台构建通过；Linux 的实际信号测试使用真实目标架构，交叉编译成功不能替代运行验证。
- 回归入口建议：[cmd/server/main_test.go](../backend/cmd/server/main_test.go)、[tracker/scheduler.go](../backend/internal/tracker/scheduler.go) 及新的生命周期回归文件；新增文件后同步架构导航。

## 建议实施顺序与接口固定边界

1. 将四项临时复现场景转成仓库内持久回归，并为停机建立可控测试；先保留失败证据，再逐项修复。
2. 优先保护管理写操作，随后统一提交结果及超时映射、资料判断，最后收口启动／停止生命周期；独立问题可以分提交处理，但不要把未修复项标记完成。
3. 尽量保留既有 API 路径、参数和 JSON 主体；本次重点是安全边界与既定语义兑现，不增加公共账号／Token、改变采集周期或引入数据库迁移。
4. 如确需新增业务码，在接口固定前列出含义、是否可重试及 Agent 行为，同步 [JSON协议](../docs/public-response.md) 和 [公共API](../docs/public-api.md)。
5. 用临时数据完成两模块 Go test/vet、相关 Node 检查、Windows与目标Linux构建／运行验收，区分已有测试通过与新回归通过。前端未改也要验证 Agent 对暂停／超时结果的兼容。
6. 全部验收通过后，再确认适合部署和固定接口；防火墙／IP 白名单由用户选定的部署边界实施，不能以应用来源校验替代网络访问控制。

## 完成与记录规则

- 五项问题及必要验证未完成前，本计划保持“待实施／实施中”，不填写完成时间、不添加完成摘要。
- 后续记录应逐项注明变更文件、测试命令、实际输出、未覆盖场景及提交引用。
- 只有五项目标实际解决且必要验证通过，才在本文件顶部追加完成摘要；原现场、原因、方案及实施历史全部保留。
- 本次仅提交审查与修复计划文档；未修改业务代码、未将临时测试加入正式套件，也未推送或部署。
