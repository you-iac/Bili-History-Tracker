# cmd/server 入口

唯一组装点：读环境变量、创建对象、启动两个服务和一个调度器。

> 与 [main.go](main.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
env / signal
     │
     ▼
main.go ──┬──► storage.Open ────────────► SQLite
          ├──► tracker.NewLog ──────────► query.log
          ├──► tracker.New + Scheduler ─► go Run(ctx)
          ├──► admin.New ────────────────► :8182
          └──► httpapi.New ──────────────► :8181
```

## 输入 / 输出 / 边界

- **输入**：`BHT_*` 环境变量、操作系统的中断信号。
- **输出**：两个监听端口、一个调度 goroutine、一个 SQLite 文件。
- **它不认识**：业务规则、SQL、B站字段；只负责创建、连接和生命周期。

## 环境变量

| 变量 | 默认值 |
|---|---|
| `BHT_ADDR` | `127.0.0.1:8181` |
| `BHT_ADMIN_ADDR` | `127.0.0.1:8182` |
| `BHT_DB` | `data/bht.db` |
| `BHT_QUERY_LOG` | 数据库同目录 `query.log` |
| `BHT_BILI_COOKIE` | 空 |
