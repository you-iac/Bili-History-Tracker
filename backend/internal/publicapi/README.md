# publicapi 模块

8181 的唯一组装入口：公共历史查询、目标状态与有界申请，以及原实时查询 API。完整协议和部署见 [公共接口说明](../../../docs/public-api.md)，模块导航见 [架构说明](../../../docs/architecture.md#module-navigation)。

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

所有请求先通过进程共享的门，再进入业务处理；结果沿原请求返回。`New` 检查配置，`Handler` 在启动时创建一次，所有客户端共享同一个实例。`Config.RequestsPerSecond` 同时限定任意滚动一秒接纳数和在途数；拒绝不排队，返回 429 和 `Retry-After`。`Config.BatchMax` 限定原始数组前 N 项，整个请求还受 64 KiB 和 20 秒预算约束。

本包不依赖 `admin`，不暴露管理表单或日志，不处理私人 Cookie，也不提供账号/令牌。长期跟踪负载仍由调度器承担，入口限流不等于存储或目标总量预算。

测试入口：[路由隔离与旧 API](routes_test.go)、[限流并发与滚动窗口](limit_test.go)、[截断、应答和副作用](targets_test.go)、[只读协议](read_test.go)。
