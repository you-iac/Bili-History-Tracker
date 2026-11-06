# storage 模块

SQLite 持久化：打开库、迁移、算指纹、读写五表与调度目标。单写者连接。

> 与 [doc.go](doc.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
tracker ──raw──► fingerprint.go
   │                  │
   │                  │ 8 字节指纹
   └──────► repo.go ◄─┘
              ▲
admin ────────┘（读详情 / 增删目标）
              │
              ▼
            db.go ──► SQLite
              │
              └── embed migrations/*.sql
```

`repo.go` 是唯一的 SQL 出口；`db.go` 只管连接、PRAGMA 和迁移。

## 输入 / 输出 / 边界

- **输入**：tracker 的 `source.Video` / `CreatorProfile` + 指纹；admin 的详情/目标参数。
- **输出**：`SaveResult`、`VideoDetail` / `CreatorDetail`、`Target` 列表；最终写入 SQLite。
- **它不认识**：B站端点、HTTP、调度循环；间隔由调用方传入（例外：`fingerprint.go` 按 B站字段投影）。

## 关键约束

- 版本行 + 当前表 + 指针在同一个事务内。
- 内容未变只更新计数；迟到响应整次丢弃。
- `MaxOpenConns(1)`：读写串行，避免 `SQLITE_BUSY`。
