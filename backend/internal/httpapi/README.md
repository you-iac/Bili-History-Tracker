# httpapi 模块

8181 只读 JSON API：路由、超时、错误映射；不写库、不触发跟踪。

> 与 [handler.go](handler.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
curl
  │ GET /api/v1/platforms/{platform}/...
  ▼
handler.go ──► Querier.GetVideo / GetCreator ──► source.Service
   │
   ├── 成功：writeJSON {"data": ...}
   └── 失败：writeError {"error": {"code": ...}}
```

## 路由

| 路径 | 作用 |
|---|---|
| `GET /healthz` | 不访问上游 |
| `GET /api/v1/platforms/{platform}/videos/{id}` | 视频元数据 |
| `GET /api/v1/platforms/{platform}/creators/{id}` | 作者资料 |

## 输入 / 输出 / 边界

- **输入**：`platform`、`id` 路径参数和 Querier 结果。
- **输出**：固定 JSON 结构；不透传上游正文。
- **它不认识**：SQLite、tracker、调度器；只读。

## 关键约束

- 每个查询 12 秒超时；错误码到 HTTP 状态的映射集中在 `writeError`。
