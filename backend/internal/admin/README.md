# admin 模块

8182 观察台：信息看板、对象详情、关注管理与采集记录。服务端渲染；页面只读本地数据库，只有命令会异步触发采集。

> 与 [doc.go](doc.go) 的包注释保持一致；页面职责和状态口径见 [观察台](../../../docs/dashboard.md)，架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
浏览器
  ├─ GET 页面 ──► server.go（路由 / 表单校验 / 渲染入口）
  │                    │
  │                    ▼
  │              pages.go + views.go（页面编排 / 视图与格式化）
  │                    │
  │                    ├──► storage/dashboard.go（只读聚合）
  │                    └──► templates/*.html + assets/*（go:embed）
  │
  └─ POST 命令 ──► server.go ──► storage（AddTarget / RemoveTarget）
                         └────► tracker.RunNow（异步查询）
```

`pages.go` 编排只读页面，`views.go` 定义视图模型、格式化、状态文案和版本字段对比，`server.go` 负责路由与命令。页面不直接拼 SQL，也不请求上游。

## 路由

| 路径 | 作用 |
|---|---|
| `GET /` | 信息总览：记录数、近期更新、问题、排行与趋势 |
| `GET /videos`、`GET /creators` | 内容库：搜索、排序、关注筛选、分页 |
| `GET /activity` | 动态与提醒：版本变化、采集异常、恢复 |
| `GET /manage` | 关注管理：添加、间隔、立即刷新、停止关注 |
| `GET /logs` | 采集记录：内存最近 500 条，可筛选分页 |
| `GET /detail/{type}/{id}` | 对象详情：指标、版本时间线、字段对比、业务快照 |
| `GET /api/metrics/{type}/{id}` | 趋势 JSON（7/30/90/全部） |
| `GET /api/targets/status` | 当前关注状态 JSON |
| `GET /assets/*` | 内嵌样式与脚本 |
| `POST /targets` | 添加并立即查询；重复添加提示已关注 |
| `POST /targets/delete` | 停止调度，保留历史 |
| `POST /targets/run` | 异步执行一次 |
| `POST /targets/interval` | 修改间隔 |

## 输入 / 输出 / 边界

- **输入**：URL 参数（`q`、`sort`、`following`、`kind`、`tab`、`days`、`page`、`seq`）；表单（`type`、`id`、`interval_hours`）。BV、UID 和可本地解析的标准链接在入口规范化，短链和任意 URL 不传给上游。
- **输出**：HTML 页面、两个 JSON 接口；副作用是写 `tracking_targets`、触发 `RunNow`、读取 storage 和内存日志。
- **它不认识**：B站端点、指纹算法、调度器内部；自身不直接请求上游。

## 关键约束

- 不设登录与权限，默认只监听回环地址。
- 浏览页面不触发采集；只有添加、刷新等命令会调用 `RunNow`。
- 状态判定在 `storage.scanContent`；阈值是 `max(间隔×2, 30 分钟) + 30 分钟`。`running` 只来自本进程的在途查询，不持久化。
- 列表只读当前字段和封面表达式，不返回整份 payload；详情快照默认收起，选中历史版本才读取对应 `seq`。
- 查询日志最多保留 500 条，重启时从文件末尾恢复；日志不是异常状态的唯一来源。
- 测试：[pages_test.go](pages_test.go) 覆盖页面、API、空库、输入规范化和停止关注；[chart_test.cjs](chart_test.cjs) 覆盖图表数据边界，用 `node --test internal/admin/chart_test.cjs` 执行。
