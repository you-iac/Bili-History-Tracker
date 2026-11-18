# Agent 视角的服务器吞吐压测

[load_test.py](load_test.py) 使用 Python 3.10+ 标准库，模拟多个 Agent 同时访问 8181 公共 API。默认只读取现有资料，不修改服务器数据。协议与流程依据 [remote 客户端](../../client/agent/internal/remote/client.go) 和 [关注核对](../../client/agent/internal/library/reconciliation.go)，相关设计见 [公共应答](../../docs/public-response.md)。回归入口为 [test_load_test.py](test_load_test.py)。

这里测量的是服务器面向活跃 Agent 的请求和业务操作吞吐。它不启动真实 Agent、不读写个人库、不模拟本地缓存命中、图片加载、分钟级后台调度或完整重试队列，不能直接换算成在线用户数。容量模式还去掉客户端节流，避免把客户端启动速度当成服务器上限。

## 先确认主机能连接 VM

新版服务器默认 `BHT_ADDR=0.0.0.0:8181`，可通过 VM IP 直连；旧二进制或启动配置可能仍显式限制本机访问。SSH 能连接 VM 并不代表主机能直接连接 8181。先在 VM 检查：

```bash
ss -lntp 'sport = :8181'
```

若监听的是 `127.0.0.1:8181`，重新编译并使用新版服务器，移除旧启动配置中的本机监听覆盖；也可显式设置 `BHT_ADDR=0.0.0.0:8181`。停止原服务后，在**原工作目录、原二进制路径和原数据库配置**下重启。下面以从 `backend/` 运行 `./bin/server` 为例，已有的 `BHT_DB` 等配置需继续保留：

```bash
BHT_ADDR=0.0.0.0:8181 BHT_API_RPS=10000 ./bin/server
```

如果使用 systemd，在该服务的 `[Service]` 配置中添加以下环境变量，再重启该服务；在 SSH 终端中执行 `export` 不会改变已经运行的服务，也不会自动传给 systemd 服务：

```ini
[Service]
Environment="BHT_ADDR=0.0.0.0:8181"
Environment="BHT_API_RPS=10000"
```

主机先验证连接：

```bash
curl http://192.168.61.128:8181/healthz
```

若服务已经监听 `0.0.0.0:8181`，仍然连接失败，核对 VM 当前 IP（`hostname -I`）和该端口的防火墙规则。管理端 `BHT_ADMIN_ADDR` 继续使用默认 `127.0.0.1:8182`。

## 直接开始

当前重点是按 ID 读取一个创作者或视频，`--mode read` 默认使用 `--scenario detail`。每次操作只发送一次详情请求，不混入首页、目录或状态请求；返回当前资料、30 日指标和第一页版本（`page=1&days=30`，不指定历史快照 `seq`）。HTTP 和业务操作均按 `detail/creator`、`detail/video` 分别统计。

在仓库根目录分别测两种详情，保留 `--mode read --duration --concurrency` 用法：

```bash
python scripts/perf/load_test.py --base-url http://192.168.61.128:8181 --mode read --kind creator --duration 60 --concurrency 32 --json-out perf-creator-32.json
python scripts/perf/load_test.py --base-url http://192.168.61.128:8181 --mode read --kind video --duration 60 --concurrency 32 --json-out perf-video-32.json
```

这表示 **32 个模拟 Agent，在同一个 60 秒窗口内持续随机读取所选类型的单目标详情**，窗口结束后不再发起 HTTP 请求，等待已经发出的请求收尾。`--kind all`（默认）从两种资料池随机取目标，每个目标被选择的概率相同，不保证类型各占一半。

预检在窗口外执行：自动发现时只扫描所选类型，每类最多取 `--max-ids` 个完整资料、最多检查前 20 页。UP 占位资料（`seq=0` 或 `recorded=false`）不进入详情测试池。使用 `--targets-file` 时先按 kind 筛选并预检所选详情；没有所选类型时报错。空库直接报错，不自动向 B 站补资料。预检会影响缓存，所以本工具的结果属于经过预检的读取负载，不能宣称为冷启动性能。

固定一个已有完整资料的 ID，可测反复读取同一目标；固定 ID 只预检详情，不查询目录或状态，也不发起采集。将示例 UID 换成测试库中已有的完整资料：

```bash
python scripts/perf/load_test.py --base-url http://192.168.61.128:8181 --kind creator --id 50329118 --duration 60 --concurrency 32 --json-out perf-one-creator.json
```

`--id` 必须配合具体 `--kind creator|video`，与 `--targets-file` 互斥，仅用于 detail 场景。资料不存在时预检报 `DETAIL_NOT_RECORDED`，不会把快速的无资料应答计为详情成功。固定 ID 更容易命中数据库/操作系统缓存，应与多 ID 随机读取分开记录。

逐档运行，观察业务成功吞吐何时停止增长，以及成功请求 P95、失败率和每秒完成数：

```bash
for kind in creator video; do
  for n in 1 2 4 8 16 32; do
    python scripts/perf/load_test.py --base-url http://192.168.61.128:8181 --kind "$kind" --duration 60 --concurrency "$n" --json-out "perf-$kind-$n.json"
  done
done
```

这是 Linux/Git Bash 循环。在 PowerShell 中可逐条运行并修改 `--concurrency`。

## 调用流程与节奏

需要复测此前的混合业务时，显式指定 `--mode read --scenario mixed`。该场景按以下权重随机选择操作，多个 Agent 同时执行；权重是明确的测试模型，不声称来自真实用户流量。每个操作内部保持顺序，发生业务失败后不继续后续读取。

| 操作 | 选择权重 | HTTP 调用 |
|---|---:|---|
| `overview` | 20% | `/creators?page=1` → `/videos?page=1`，与 Agent 首页一致 |
| `catalog` | 30% | 随机视频/UP 目录，自动发现时随机前 1～3 个有效页 |
| `detail` | 30% | 随机完整资料的 `/detail/{kind}/{id}?page=1&days=30` |
| `refresh` | 20% | 精确目标状态 → 有成功资料时读取详情；这里只模拟只读部分，不补申请 |

详情/状态目标从资料池随机选取。使用自定义目标文件时目录测试固定第一页，因为预检不会额外读取目录总页数。所有请求都访问服务器，没有客户端缓存。`/`、`/healthz` 和 Agent 未使用的独立指标接口不进入混合负载，也不混入混合吞吐总计。

```text
[多个模拟 Agent] --同窗混合操作--> [8181 公共 API] --> [本地资料与数据库]
       |                              |
       +--记录操作耗时                 +--记录每次 HTTP、JSON 业务结果与阶段
```

| 节奏 | 行为 | 用途 |
|---|---|---|
| `--pacing capacity`（默认） | 每个 Agent 顺序持续请求，关闭 150ms 节流和限流冷却 | 测配置约束下的处理容量；限流拒绝不计成功 |
| `--pacing agent` | 同一个 Agent 的请求启动至少间隔 150ms；收到限流后按 retry 冷却，至少 1 秒 | 测接近当前 Agent 调用节奏的服务表现 |

`agent` 模式的冷却仅影响该模拟 Agent，多个 Agent 互不共享客户端冷却。两种模式均不自动重发失败请求。单接口/流程定位可使用 `--scenario overview|catalog|detail|refresh`；默认 `detail`。`--kind/--id` 只作用于 detail，不能在混合或其他场景中使用。

## HTTP 基线与性能定位

独立运行健康检查，测量相同网络与并发下的轻量 HTTP/JSON 路径；不发现目标、不读取资料数据库，空库也可测：

```bash
python scripts/perf/load_test.py --base-url http://192.168.61.128:8181 --scenario health --duration 60 --concurrency 32 --json-out perf-http-baseline.json
```

健康检查同样经过全局限流，测处理能力需先核对服务启动日志的 RPS 配置。基线与详情分开运行，配置和网络保持一致。报告记录 `transport.protocol=HTTP/1.1` 和连接复用；每个 Agent 复用一个 HTTPConnection，完整读取应答后继续下一次请求，不为每次请求主动创建新连接。响应体字节吞吐不含 HTTP 头部、TCP 或 TLS 开销。

健康检查与详情的 SQL、响应体大小、JSON 编解码成本均不同，吞吐差距不能直接换算成 HTTP 开销。先比较并发阶梯、成功请求 P95、响应体大小与字节吞吐，再配合服务端 CPU/阻塞剖析、SQL 耗时和连接池等待统计。当前 [数据库连接池](../../backend/internal/storage/db.go) 只有一条连接；[详情读取](../../backend/internal/publicapi/read.go) 组合内容、版本、指标和目标状态，多次读取可能排队。Go 的 [诊断说明](https://go.dev/doc/diagnostics) 提供了 profiling/tracing 的定位方法。

HTTP 有解析、头部和传输成本，但当前没有证据证明它主导秒级详情延迟。更换传输协议仍需执行相同的数据库查询。现阶段保留 HTTP 并定位业务成本；只有测量证明传输或序列化主导后再评估压缩、字段裁剪或成熟协议方案，自研协议还需要维护消息边界、错误语义、超时和版本兼容。

## 申请与批量诊断

申请场景必须显式指定目标文件，格式为非空 JSON 数组，不允许重复项，ID 使用字符串：

```json
[
  {"type": "video", "id": "BV17x411w7KC"},
  {"type": "creator", "id": "123"}
]
```

| 模式 | 行为与解释 |
|---|---|
| `reconcile` | 每个目标最多消费一次：先查询状态，active/paused 尊重现状，不 POST；absent 才申请。结果未知或忙时最多再查一次状态，不自动重发 POST。资料池耗尽可提前结束，报告未消费数及中途未完成操作。 |
| `write` | 已有任务申请的诊断：预检要求全部目标有完整资料且 active，再重复单项 POST。确认成功数不等于新增落库数。 |
| `batch` | 公共服务器批量接口诊断，当前 Agent 不使用该接口。与 write 相同的预检；逐项核验身份、index、业务码和最终任务状态。忽略项、截断或部分失败不会计为整批成功。 |

```bash
# 会对 absent 目标真正发起申请，可能访问 B 站并创建资料/任务
python scripts/perf/load_test.py --mode reconcile --targets-file targets.json --duration 60 --concurrency 4 --pacing agent --json-out perf-reconcile.json

# 已有任务的确认吞吐诊断，不做隐式 warmup
python scripts/perf/load_test.py --mode write --targets-file active-targets.json --duration 30 --concurrency 8 --json-out perf-existing.json

# 批量诊断：资料池数量至少等于 batch-size
python scripts/perf/load_test.py --mode batch --targets-file active-targets.json --batch-size 10 --duration 30 --concurrency 4 --json-out perf-batch.json
```

首次申请测试需要足够多的、真实有效且尚无任务的目标。`reconcile` 中 active/paused 的核对完成数单独标记为 `ALREADY_ACTIVE/PAUSED_RESPECTED`，不能算新增任务。POST 超时后若状态核对恢复，操作记为 `RECOVERED_BY_STATUS`，原 POST 的失败、stage/outcome 仍保留在 HTTP 统计中；该确认不伪造成成功的 POST 或确定的 created。

`created/existing` 来自服务器应答，表示申请确认结果；它们不是数据库事务/SQL 写入次数。有限目标集短时跑完只能说明该组目标的表现，不能作为持续新增吞吐。重复运行同一组 reconcile 目标通常会走 active 分支；要比较首次申请，应恢复测试数据库或换用新的有效目标。管理员并发修改任务也可能改变预检后的分支，需结合实际结果分布解释。

旧版 `--mode all` 和 `--no-warmup` 已移除，避免一次命令隐式混入申请、B 站请求及只读吞吐。读取与申请需要分别运行、分别解释。

## 统计口径

HTTP200 不是成功依据。脚本同时检查 HTTP、JSON 媒体类型、业务 `ok/code/error`、所需结构和详情/状态目标身份。限流、无资料、错误身份、非 JSON 和超时均不计业务成功。状态查询正确返回 absent/paused 属于一次成功读取，不能当成成功跟踪申请。

输出分成 **HTTP 请求** 和 **Agent 业务操作** 两组，各组有一个 TOTAL 和各项统计，不能再次把 TOTAL 与明细相加。首页操作成功需要两次目录读取都成功；请求成功数不能直接当成完整业务操作成功数。

| 字段 | 口径 |
|---|---|
| `rps` | 测量窗口内完成的请求/操作数 ÷ 窗口秒数，含失败 |
| `goodput` | 窗口内业务成功的请求/操作数 ÷ 窗口秒数，优先看此值 |
| `done` / `completed` | 全部完成数，包含收尾；不能拿它除配置窗口推算 RPS |
| `tail` / `tail_completed` | 窗口之后完成的数量，不计窗口 RPS/goodput |
| `actual_s`、`drain_s` | 实际负载耗时（不含预检/合并统计）与超出配置窗口的收尾时间 |
| `drained_rps/goodput` | 包含收尾的完成/成功数 ÷ 实际耗时，单独报告 |
| `ok` / `ok_rate` | 全部完成数中的成功比例，包含收尾；JSON 另有 `window_ok_rate` |
| `p50/p95/p99`、`max` | 全部完成请求/操作的耗时，包含失败和收尾；小样本 P99 不稳定 |
| `ok_p95` / `success_latency_ms` | 仅成功请求/操作的延迟，避免快速限流掩盖慢查询 |
| `response_bytes/window_response_bytes/mean_response_bytes` | 全程/窗口响应体字节数与全程平均大小，不含 HTTP 头部或 TCP/TLS 开销 |
| `incomplete_operations` | 窗口关闭时还有后续步骤未发出的操作，不计为已完成成功，也不虚构 HTTP 错误 |
| `confirmed_targets_s` | 窗口内 POST 应答确认成功的目标数/s；批量按目标计，不按批次计 |
| `stages/outcomes` | 服务端返回的申请阶段与结果分类；批量逐项目业务码见 `target_codes` |
| `per_second` | JSON 中每个窗口秒桶的完成/成功数量；最后一桶可能不足一秒，是数量而非归一化速率 |
| `failure_examples` | 每个请求/操作类别最多 3 个失败样本，含 HTTP、业务码、阶段、结果和错误文字 |

HTTP 延迟包括传输、服务器等待和 JSON 校验，不包含客户端节流；业务操作延迟包含操作内的节流等待和所有步骤。默认 socket 超时参考 Agent：读取 22 秒、状态 8 秒、申请 55 秒；它限制单次网络阻塞，不是严格的整体请求 deadline。`--timeout` 可统一覆盖。窗口结束不发送下一步，但已经发出的请求可能继续等待应答。

遇到限流应先区分测试目标：保留生产配置可测接纳策略；测处理容量则在测试实例中调高实际生效的 `BHT_API_RPS`（最大 10000）。批量诊断还需核对 `BHT_API_BATCH_MAX`（默认 10）。不自动更改服务端配置，使用空库也不能测出有代表性的数据库查询吞吐。

## 复测与验证

同一轮对比固定服务版本、数据库快照/记录规模、限流配置、VM vCPU/内存、测试机和网络条件。不要在采集器负载不同或管理员持续修改目标的情况下直接比较。使用测试实例；复制 SQLite 数据时通过 backup 或停服取得一致快照，不只复制运行中的主文件。

在 VM 上同步记录 CPU、内存与磁盘指标，PID 替换成实际进程：

```bash
pidstat -u -r -d -p <PID> 1
```

报告含 UTC 测量开始/结束时间，便于对应监控。Python 单进程也可能先达到瓶颈：同时观察压测机 CPU；服务器吞吐平台期若伴随压测机单核饱和，应增加独立负载发生器并汇总同窗结果后再判断。闭环并发模型会在服务变慢时降低请求发起速率，它测量固定活跃 Agent 数下的吞吐与等待，不能替代固定到达率的过载测试或在线用户容量估算。

需要定位 SQL 计算热点或连接等待时，使用 [服务端 pprof 与火焰图](../../docs/performance-profiling.md) 在重放负载的同时采样。已结束且未采样的测试不能补采 CPU 数据。

本地自动化验证只使用环回地址，不访问 VM、B 站或真实数据库：

```bash
python -m unittest discover -s scripts/perf -p 'test_*.py' -v
python scripts/perf/load_test.py --help
```

退出码 0 表示负载执行并生成统计，不代表业务零失败；参数/预检失败、压测器内部错误或没有完成任何 HTTP 请求返回 2。真实容量需要在目标 VM 上运行，模拟服务测试不提供性能结论。
