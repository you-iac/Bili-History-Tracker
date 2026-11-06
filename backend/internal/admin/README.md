# admin 模块

8182 本地后台：目标管理、查询日志、入库详情；服务端渲染。

> 与 [doc.go](doc.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
浏览器
  │ 表单 / GET
  ▼
server.go ──┬──► storage（读详情/统计，写 tracking_targets）
            ├──► tracker.RunNow（异步）
            └── go:embed ──► templates/*.html
```

`server.go` 只做三件事：解析表单、调用 storage/tracker、渲染模板。

## 路由

| 路径 | 作用 |
|---|---|
| `GET /` | 统计、目标、最近 100 条日志 |
| `GET /detail/{type}/{id}` | 当前字段 + 最新 payload |
| `POST /targets` | 添加并立即查询 |
| `POST /targets/delete` | 停止调度，保留历史 |
| `POST /targets/run` | 异步执行一次 |
| `POST /targets/interval` | 修改间隔 |

## 输入 / 输出 / 边界

- **输入**：表单（`type`、`id`、`interval_hours`）、URL 参数。
- **输出**：HTML 页面；副作用是写 `tracking_targets`、触发 `RunNow`、读 storage。
- **它不认识**：B站端点、指纹算法、调度器内部；自身不直接请求上游。

## 关键约束

- 不设登录与权限，默认只监听回环地址。
- 间隔限制 0.1–720 小时；UID/BV 格式在入口校验。
