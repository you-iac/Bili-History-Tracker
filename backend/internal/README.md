# backend/internal 模块地图

`internal` 下按职责分包，唯一组装点是 `cmd/server`。图中箭头表示运行时调用或读写，Go 包的导入约束见下方“依赖规则”。
每个子目录的 `README.md` 以一张子模块协作图为主，说明输入、输出和边界。

## 总览

```text
+--------------------------+                 +--------------------------+
| admin :8182              |                 | publicapi :8181          |
| HTML / private UI actions|                 | routes / rate / batch    |
+--------------------------+                 +--------------------------+
             |                                            |
        read / command                       read / accept / live query
             |                                            |
             v                                            v
+--------------------------+                 +--------------------------+
| storage / tracker       | <--------------- | public read / targets    |
| history / sampling      |  read / accept   | httpapi: live query      |
+--------------------------+                 +--------------------------+
             |                                            |
        SQL read/write                              platform query
             v                                            v
+--------------------------+                 +--------------------------+
| SQLite                   |                 | source / provider        |
| bht.db                   |                 | Bilibili HTTPS endpoints |
+--------------------------+                 +--------------------------+
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
| `publicapi` | 8181公共路由、共享限流和有界目标申请 | [README](publicapi/README.md) |
| `httpapi` | 8181保留的实时查询内核，由publicapi挂载 | [README](httpapi/README.md) |

入口与组装见 [backend/cmd/server/README.md](../cmd/server/README.md)。

图中`httpapi`是原实时查询内核；当前8181由`publicapi`在限流后分发历史读取、跟踪申请和实时查询。它不挂载后台处理器，两端共享存储和采集实例。

## 依赖规则

- 入口层（`admin`、`publicapi`、`httpapi`）只依赖用例层或契约层；
- `tracker` 依赖 `source` 与 `storage`；`discovery` 通过小接口调用本地读取和 tracker 的发现、导入能力；
- `storage` 依赖 `source` 的数据类型；
- `provider/*` 只依赖 `source`；
- 任何包都不反向依赖 `cmd/server`，组装集中在入口。

## 数据流向

- 写：`publicapi` → `tracker` → `storage`；`admin` → `tracker` → `storage`；`scheduler` → `tracker` → `storage`。
- 读：`publicapi` → `storage` / `httpapi` → `source`；常规页面 `admin` → `storage`；作者可添加视频 `admin` → `discovery` → `storage` / `tracker` → `source`。
- 上游：所有取数最终经 `source` 分发到 `provider/bilibili`。
