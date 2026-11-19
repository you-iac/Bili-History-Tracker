# publicapi 模块

8181 的唯一组装入口：公共历史查询、目标状态与有界申请，以及原实时查询 API。完整协议和部署见 [公共接口说明](../../../docs/public-api.md)，模块导航见 [架构说明](../../../docs/architecture.md#module-navigation)。

[client_history.go](client_history.go) 提供有界批量7日历史、历史专用读取与本地失败兜底；目录可只枚举身份，不为当前资料查询上游。客户端/服务器分工见 [本地优先资料](../../../docs/local-first-metadata.md)。

```text
+------------------------+      request      +------------------------+
| Agent / API caller     | ----------------> | limit.go               |
+------------------------+                   | rolling RPS / in-flight|
                                             +------------------------+
                                                          |
                                               admit without waiting
                                                          v
                                             +------------------------+
                                             | server.go              |
                                             | explicit public routes |
                                             +------------------------+
                                                          |
                                              route to allowed handler
                                                          v
+------------------------+  +------------------------+  +------------------------+
| read.go                |  | targets.go / input.go  |  | httpapi.New            |
| storage: saved history |  | tracker: first N only  |  | source: live queries   |
+------------------------+  +------------------------+  +------------------------+
```

所有请求先通过进程共享的门，再进入业务处理；结果沿原请求返回。`New` 检查配置，`Handler` 在启动时创建一次，所有客户端共享同一个实例。`Config.RequestsPerSecond` 同时限定任意滚动一秒接纳数和在途数；拒绝不排队，返回HTTP200及JSON `ok:false/code:RATE_LIMITED` 和 `Retry-After`。`Config.BatchMax` 限定原始数组前 N 项，整个请求还受64 KiB和25秒接纳预算约束；已取得有效资料的当前项允许独立有界收尾，最长业务处理约36秒。应答带 `stage/outcome`，阶段与错误原因分开，见 [阶段设计](../../../docs/request-stages.md) 和 [阶段回归](request_stages_test.go)。

本包不依赖 `admin`，不暴露管理表单或日志，不处理私人 Cookie，也不提供账号/令牌。长期跟踪负载仍由调度器承担，入口限流不等于存储或目标总量预算。

业务协议见 [JSON应答](../../../docs/public-response.md)；[response.go](response.go) 提供目标/资料独立状态，[legacy.go](legacy.go) 只适配实时上游业务结果，数据库内部故障保留500。

详情只读取一次目标摘要，复用请求内资料组装应答，同时重新确认任务启停和运行状态；观测字段沿用本次摘要，不跨请求缓存。调用与 SQL 成本见 [详情性能](../../../docs/detail-query-performance.md)，回归见 [detail_query_test.go](detail_query_test.go)。

测试入口：[业务组合与整数精度](response_test.go)、[路由隔离与旧 API](routes_test.go)、[限流并发与滚动窗口](limit_test.go)、[截断、应答和副作用](targets_test.go)、[只读协议](read_test.go)。
