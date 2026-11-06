# provider/bilibili 模块

B站网页端点适配器：把上游 JSON 转成 `source` 的统一结构，并保留原始 `data`。

> 与 [bilibili.go](bilibili.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../../docs/architecture.md)。

## 子模块协作

```text
source.Service
      │ GetVideo / GetCreator / GetCreatorVideos
      ▼
 bilibili/
   ├─ bilibili.go ─┐
   ├─ creator.go ──┼──► http.go ──► api.bilibili.com
   └─ list.go ─────┘
```

`http.go` 是所有请求的唯一出口；三个查询文件只负责参数校验和解析各自的 `data`。

## 文件与端点

| 文件 | 端点 |
|---|---|
| [bilibili.go](bilibili.go) | `/x/web-interface/view` |
| [creator.go](creator.go) | `/x/web-interface/card` |
| [list.go](list.go) | `/x/space/arc/search`、`/x/polymer/web-space/seasons_series_list` |
| [http.go](http.go) | 共用请求与响应校验 |

## 输入 / 输出 / 边界

- **输入**：BV、UID、context，以及 B站 HTTP 响应。
- **输出**：`Video`、`CreatorProfile`、`CreatorVideo`、`*source.Error`；`Raw` 保留原始 `data`。
- **它不认识**：数据库、调度器、8181/8182 服务；Cookie 只来自 `BHT_BILI_COOKIE`。

## 关键约束

- 端点固定，不接受调用方 URL；不跟随重定向、不重试。
- 响应体上限 2 MiB；HTTP 200 与业务 `code=0` 分开检查。
