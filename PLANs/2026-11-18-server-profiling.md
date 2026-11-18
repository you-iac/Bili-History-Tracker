# 服务端 pprof 与火焰图诊断

## 完成摘要

- 状态：已完成。
- 创建时间：2026-11-18 16:38（Asia/Shanghai）。
- 完成时间：2026-11-18 16:48（Asia/Shanghai）。

已实现默认关闭、仅接受回环 IP 的独立 [pprof 入口](../backend/cmd/server/profiling.go)，提供 CPU、堆、goroutine、阻塞/锁与 trace，阻塞和锁采样显式配置，诊断服务纳入主 context 与统一关闭流程，不挂载 8181/8182。新增 [配置、实际 CPU/heap 数据与路由隔离测试](../backend/cmd/server/profiling_test.go)，后端 `go test ./...` 和 `go vet ./...` 通过，Linux amd64 构建成功；文档相对链接与 diff 空白检查通过。[性能诊断文档](../docs/performance-profiling.md)、命令模块说明、架构导航和压测说明已同步，解释已结束负载不能补采、普通优化构建、负载重放、独立采样与 SSH 火焰图。尚未采集真实 VM profile，Linux 构建不代表已完成真实 VM 运行验收；无需凭空生成火焰图。原方案完整保留，按用户提供的当前约定不迁移归档。

## 原计划

- 创建时间：2026-11-18 16:38（Asia/Shanghai）。
- 状态：实施中。

## 目标与边界

为已经完成吞吐测试的 Go 服务提供可实际使用的 CPU、堆、goroutine、阻塞/锁和 trace 采样入口，指导在重放详情负载时采样并查看火焰图。默认关闭，启用后仅绑定独立回环地址；不挂载到 8181/8182，不改变查询、数据库或默认采样率。

## 方案与验收

1. 新增可选 `BHT_PPROF_ADDR`，校验为回环 IP；阻塞与锁采样分别显式开启，不将 CPU 和等待时间混为同一指标。
2. 诊断服务与已有服务共用取消与关闭流程，支持 CPU 长采样请求。
3. 自动测试默认关闭、配置校验、GET 入口、CPU/堆数据及公共端口隔离。
4. [技术文档](../docs/performance-profiling.md) 记录编译、负载重放、采样、SSH 隧道和 Flame Graph 查看；说明 GDB/Delve 与 pprof 的适用边界。
5. 已结束的压测不能补采 CPU 样本；真实 VM 的火焰图需用户在诊断版本下重跑负载，不能把本地模拟验证当成容量结论。
