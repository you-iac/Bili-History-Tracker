# 服务端 pprof 与火焰图

Go 服务的吞吐定位先使用 pprof：CPU profile 找计算热点，block/goroutine 找等待，heap/allocs 看内存。GDB 用于断点、崩溃等调试，不能直接提供已结束压测的 CPU 火焰图；Go 断点调试优先考虑 Delve，见 [Go 官方说明](https://go.dev/doc/gdb)。

实现位于 [profiling.go](../backend/cmd/server/profiling.go)，由 [main](../backend/cmd/server/main.go) 启用并统一关闭；配置、数据与路由隔离测试见 [profiling_test.go](../backend/cmd/server/profiling_test.go)。模块导航见 [architecture.md](architecture.md)。详情负载发生器与统计口径见 [压测说明](../scripts/perf/README.md)。

## 配置与边界

| 变量 | 默认值 | 含义 |
|---|---|---|
| `BHT_PPROF_ADDR` | 空，关闭 | 独立诊断监听地址，只接受回环 IP，如 `127.0.0.1:6060` 或 `[::1]:6060`，不接受主机名/通配/局域网 IP |
| `BHT_PPROF_BLOCK_RATE` | `0`，关闭 | 阻塞采样平均间隔，单位纳秒；如 `1000000` 为 1ms，`1` 接近每次阻塞均记录 |
| `BHT_PPROF_MUTEX_FRACTION` | `0`，关闭 | 平均每 N 次互斥锁竞争采一次，如 `5`；不是毫秒 |

采样参数只能是非负整数；非零采样参数必须同时启用诊断地址。两个采样率只在启用诊断时应用，不改变正常运行的默认采样。诊断入口不挂载到 8181 或 8182，也不经过业务限流。已启用的 HTTP 服务共用主取消信号和 45 秒关闭预算，长 CPU/trace 采样可因停机取消。

```text
[主机压测器] --详情请求--> [8181 publicapi] --> [storage / SQLite]
                                |
                         进程中的执行与等待
                                |
                                v
[VM 采样终端] ----------> [127.0.0.1:6060 pprof]
                                |
                                v
                        [本地 profile 文件]
                                |
                                v
                        [go tool pprof 查看]
```

pprof 使用独立路由树，提供 `GET /debug/pprof/`、`profile`、`heap`、`allocs`、`goroutine`、`block`、`mutex`、`cmdline`、`symbol` 和 `trace` 等标准入口。它们包含进程诊断信息；主机查看通过 SSH 转发完成。CPU、阻塞、锁和 trace 分轮采集，避免相互干扰。仅启用入口不会自动开始 CPU 采样，必须实际请求 `/profile`。参数与采样差异见 [net/http/pprof](https://pkg.go.dev/net/http/pprof) 和 [Go 诊断指南](https://go.dev/doc/diagnostics)。

## 一轮 CPU 采样

已结束且未采样的压测无法补采 CPU/阻塞样本，需在诊断版本下短暂重放负载。当前 heap/goroutine 快照可以采，但不代表刚才压测时的峰值。

**VM 终端一：**从原来的 `backend/` 工作目录编译、启动，继续使用原 `BHT_DB` 等配置。先停止原服务以释放端口，再运行：

```bash
cd ~/workspace/Bili-History-Tracker/backend
mkdir -p bin/profiles
go build -o bin/server ./cmd/server
BHT_API_RPS=10000 BHT_PPROF_ADDR=127.0.0.1:6060 ./bin/server
```

为保持符号与运行行为可比，使用普通优化构建，不加 `-gcflags="all=-N -l"`，也不剥离调试信息。保留本次运行的二进制用于分析。入口新增了同包实现文件，使用 `go build ./cmd/server` 或 `go run ./cmd/server`；只执行 `go run cmd/server/main.go` 不会编译同目录其他文件。

启动应显示 `pprof listening on http://127.0.0.1:6060/debug/pprof/`。CPU 这一轮保留默认 block/mutex 关闭；如果此前导出了这些采样变量，先清除它们。

**主机终端：**重放一种详情负载，90 秒窗口给采样留出启动时间：

```bash
python scripts/perf/load_test.py --kind creator --duration 90 --concurrency 32 --json-out perf-creator-profile.json
```

**VM 终端二：**确认主机已通过预检、开始负载后，等待约 5～10 秒再采 30 秒 CPU：

```bash
cd ~/workspace/Bili-History-Tracker/backend
curl --noproxy '*' -fsS --max-time 45 -o bin/profiles/creator-cpu.pb.gz 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'
go tool pprof -top ./bin/server bin/profiles/creator-cpu.pb.gz
go tool pprof -top -cum ./bin/server bin/profiles/creator-cpu.pb.gz
```

`curl` 在采样期间等待是正常行为；`--noproxy '*'` 避免本地采样受 SSH 会话中的代理变量影响。每种负载单独存档，测视频时改为 `--kind video` 并将文件名改为 `video-cpu.pb.gz`。CPU 采样期间不要同时运行 block/mutex delta 或 trace 采集。记录服务提交、原二进制、负载 JSON 与采样时间；后台采集器也在同一进程中，profile 会包含其工作。

## 浏览火焰图

在 VM 终端二启动查看器：

```bash
go tool pprof -http=127.0.0.1:8088 -no_browser ./bin/server bin/profiles/creator-cpu.pb.gz
```

主机另开终端，建立 SSH 转发并保持运行：

```bash
ssh -N -L 18088:127.0.0.1:8088 opt@192.168.61.128
```

主机浏览器打开 `http://127.0.0.1:18088`，在 View 菜单选择 Flame Graph。若图形视图提示缺少 `dot`，安装 Graphviz；也可先用 `-top/-cum/-list` 文本视图定位，不依赖 Graphviz。

矩形越宽表示该调用路径累计采样越多，纵向是调用深度，不是时间轴。CPU 图表示正在运行的计算时间，数据库连接等待、网络阻塞或 goroutine 调度等待不能直接从 CPU 图宽度推出。优先观察 `storage.Content/Catalog`、`database/sql`、`modernc.org/sqlite`、JSON 编码与 runtime/GC 的分布，以实际样本为准。

## 阻塞、锁、内存与调度

另起一轮启用阻塞采样，重启命令示例：

```bash
BHT_API_RPS=10000 BHT_PPROF_ADDR=127.0.0.1:6060 BHT_PPROF_BLOCK_RATE=1000000 ./bin/server
```

重放同一种负载，在其窗口内采集 30 秒阻塞增量：

```bash
curl --noproxy '*' -fsS --max-time 45 -o bin/profiles/creator-block.pb.gz 'http://127.0.0.1:6060/debug/pprof/block?seconds=30'
go tool pprof -top -cum ./bin/server bin/profiles/creator-block.pb.gz
```

重点检查数据库连接获取栈（如 `database/sql.(*DB).conn`）和通道等待。阻塞累计时间是多个 goroutine 的等待之和，可以超过采样的 30 秒，不等同于 CPU 时间。block 是已记录阻塞事件的采样，结合运行中的 goroutine 快照与连接池等待统计解释；不能仅看某个 runtime 函数名字就认定根因。

锁采样另轮使用 `BHT_PPROF_MUTEX_FRACTION=5`，采集 `/debug/pprof/mutex?seconds=30`；未开启采样就读取 block/mutex 通常不能提供有效竞争数据。环境变量需在启动前设置，不能在另一个 SSH 终端 export 后让现有进程自动生效。

需要当前快照时可分别执行：

```bash
curl --noproxy '*' -fsS -o bin/profiles/heap.pb.gz http://127.0.0.1:6060/debug/pprof/heap
curl --noproxy '*' -fsS -o bin/profiles/goroutines.txt 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=2'
```

heap 默认查看仍存活的内存；`allocs` 看累计分配。heap 快照默认不强制 GC，避免把 GC 本身混入其他采样。

若要确认调度或等待时序，在独立一轮负载中采 5 秒 trace：

```bash
curl --noproxy '*' -fsS --max-time 15 -o bin/profiles/trace.out 'http://127.0.0.1:6060/debug/pprof/trace?seconds=5'
go tool trace -http=127.0.0.1:8088 bin/profiles/trace.out
```

查看器端口和 SSH 转发可复用，但同一时间只启动一个占用 8088 的查看器。采样结束后移除 `BHT_PPROF_*` 配置并重启即可关闭诊断服务和额外采样。
