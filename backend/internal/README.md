# backend/internal 模块地图

`internal` 下按职责分包，依赖方向自上而下，唯一组装点是 `cmd/server`。
每个子目录的 `README.md` 以一张子模块协作图为主，说明输入、输出和边界。

## 总览

```text
        ┌──────────────────┐        ┌──────────────────┐
        │  admin  :8182    │        │  httpapi :8181   │
        │  HTML 管理后台    │        │  JSON 查询 API    │
        └────────┬─────────┘        └────────┬─────────┘
                 │ 读写库                   │ 只读查询
                 ▼                          │
        ┌──────────────────┐                │
        │  tracker         │                │
        │  查询/调度/发现   │                │
        └───┬──────────┬───┘                │
            │          │                    │
   ┌────────▼───┐  ┌───▼──────────┐         │
   │  storage   │  │  source      │◄────────┘
   │  SQLite    │  │  + provider  │──────► api.bilibili.com
   └────────────┘  └──────────────┘
```

## 模块导航

| 模块 | 一句话职责 | 文档 |
|---|---|---|
| `source` | 平台无关的查询契约与能力分发 | [README](source/README.md) |
| `provider` | 平台适配器目录 | [README](provider/README.md) |
| `provider/bilibili` | B站网页端点适配 | [README](provider/bilibili/README.md) |
| `storage` | SQLite 持久化、指纹、五表读写 | [README](storage/README.md) |
| `tracker` | 查询用例、循环调度、合集发现 | [README](tracker/README.md) |
| `admin` | 8182 本地 HTML 后台 | [README](admin/README.md) |
| `httpapi` | 8181 只读 JSON API | [README](httpapi/README.md) |

入口与组装见 [backend/cmd/server/README.md](../cmd/server/README.md)。

## 依赖规则

- 入口层（`admin`、`httpapi`）只依赖用例层或契约层；
- `tracker` 依赖 `source` 与 `storage`；
- `storage` 依赖 `source` 的数据类型；
- `provider/*` 只依赖 `source`；
- 任何包都不反向依赖 `cmd/server`，组装集中在入口。

## 数据流向

- 写：`admin` → `tracker` → `storage`；`scheduler` → `tracker` → `storage`。
- 读：`httpapi` → `source`；`admin` → `storage`。
- 上游：所有取数最终经 `source` 分发到 `provider/bilibili`。
