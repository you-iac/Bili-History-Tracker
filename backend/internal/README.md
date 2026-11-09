# backend/internal 模块地图

`internal` 下按职责分包，唯一组装点是 `cmd/server`。图中箭头表示运行时调用或读写，Go 包的导入约束见下方“依赖规则”。
每个子目录的 `README.md` 以一张子模块协作图为主，说明输入、输出和边界。

## 总览

```text
            ┌────────────────────────────┐                    ┌────────────────────────────┐
            │ admin  :8182               │                    │ httpapi  :8181             │
  ┌─────────│ 观察台页面                 │                    │ JSON 查询 API              │
  │         └────────────────────────────┘                    └────────────────────────────┘
  │                        │                                                 │
  │                        │ 立即查询                                        │
  │                        │                                                 │
  │                        ▼                                                 │
  │         ┌────────────────────────────┐                                   │
  │读写库   │ tracker                    │                                   │ 只查上游
  │         │ 查询 / 调度 / 发现         │────────┐                          │
  │         └────────────────────────────┘        │                          │
  │                        │                      │                          │
  │                        │ 保存 / 采样          │                          │
  │                        │                      │                          │
  │                        ▼                      │平台查询                  ▼
  │         ┌────────────────────────────┐        │           ┌────────────────────────────┐
  │         │ storage                    │        │           │ source + provider          │
  └────────►│ 内容 / 版本 / 指标 / 目标  │        └──────────►│ 能力分发 / 平台适配        │
            └────────────────────────────┘                    └────────────────────────────┘
                           │                                                 │
                           │ SQL                                             │ HTTPS GET
                           │                                                 │
                           ▼                                                 ▼
            ┌────────────────────────────┐                    ┌────────────────────────────┐
            │ SQLite                     │                    │ api.bilibili.com           │
            │ data/bht.db                │                    │ B站网页端点                │
            └────────────────────────────┘                    └────────────────────────────┘
```

## 模块导航

图例统一使用等宽字符框图：方框内为模块名与职责，箭头旁说明调用或数据访问；返回值沿调用链回传。`httpapi` 的“只查上游”表示不写库，也不读取 SQLite。

| 模块 | 一句话职责 | 文档 |
|---|---|---|
| `source` | 平台无关的查询契约与能力分发 | [README](source/README.md) |
| `provider` | 平台适配器目录 | [README](provider/README.md) |
| `provider/bilibili` | B站网页端点适配 | [README](provider/bilibili/README.md) |
| `storage` | SQLite 持久化、指纹、五表读写、看板查询 | [README](storage/README.md) |
| `tracker` | 查询用例、循环调度、显式导入 | [README](tracker/README.md) |
| `discovery` | 作者视频融合、关注过滤、分页与导入任务 | [README](discovery/README.md) |
| `admin` | 8182 观察台页面、详情与命令 | [README](admin/README.md) |
| `httpapi` | 8181 只读 JSON API | [README](httpapi/README.md) |

入口与组装见 [backend/cmd/server/README.md](../cmd/server/README.md)。

## 依赖规则

- 入口层（`admin`、`httpapi`）只依赖用例层或契约层；
- `tracker` 依赖 `source` 与 `storage`；`discovery` 通过小接口调用本地读取和 tracker 的发现、导入能力；
- `storage` 依赖 `source` 的数据类型；
- `provider/*` 只依赖 `source`；
- 任何包都不反向依赖 `cmd/server`，组装集中在入口。

## 数据流向

- 写：`admin` → `tracker` → `storage`；`scheduler` → `tracker` → `storage`。
- 读：`httpapi` → `source`；常规页面 `admin` → `storage`；作者可添加视频 `admin` → `discovery` → `storage` / `tracker` → `source`。
- 上游：所有取数最终经 `source` 分发到 `provider/bilibili`。
