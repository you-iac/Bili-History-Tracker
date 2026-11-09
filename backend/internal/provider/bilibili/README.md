# provider/bilibili 模块

B站网页端点适配器：把上游 JSON 转成 `source` 的统一结构，准备业务快照；视频仅移除顶层 `ugc_season`，作者资料保留原始 `data`。

> 与 [bilibili.go](bilibili.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../../docs/architecture.md)。

## 子模块协作

```text
                                ┌────────────────────────────┐
                                │ source.Service             │
                                │ 视频 / 作者 / 列表请求     │
                                └────────────────────────────┘
                                               │
                                               │
               ┌───────────────────────────────┼───────────────────────────────┐
               ▼                               ▼                               ▼
┌────────────────────────────┐  ┌────────────────────────────┐  ┌────────────────────────────┐
│ bilibili.go                │  │ creator.go                 │  │ list.go                    │
│ BV 校验 / GetVideo         │  │ UID 校验 / GetCreator      │  │ 投稿 / 公开合集发现        │
└────────────────────────────┘  └────────────────────────────┘  └────────────────────────────┘
               │                               │                               │
               │                               │                               │
               └───────────────────────────────┼───────────────────────────────┘
                                               ▼
                                ┌────────────────────────────┐
                                │ http.go                    │
                                │ 请求 / 状态与业务码校验    │
                                └────────────────────────────┘
                                               │
                                               │ HTTPS GET
                                               │
                                               ▼
                                ┌────────────────────────────┐
                                │ api.bilibili.com           │
                                │ 固定端点 / 返回 data       │
                                └────────────────────────────┘
```

`http.go` 是所有请求的唯一出口，返回原始 `data`；三个查询文件负责参数校验和解析各自的 `data`。`GetVideo()` 校验身份后调用 [snapshot.go](snapshot.go) 的 `videoSnapshot()` 准备存档内容，再沿调用链返回 `Raw`。过滤实现由本模块的 `videoSnapshotVersion` 常量选择，不随数据传递或入库。

## 文件与端点

| 文件 | 端点 |
|---|---|
| [bilibili.go](bilibili.go) | `/x/web-interface/view` |
| [creator.go](creator.go) | `/x/web-interface/card` |
| [list.go](list.go) | `/x/space/arc/search`、`/x/polymer/web-space/seasons_series_list` |
| [http.go](http.go) | 共用请求与响应校验 |
| [snapshot.go](snapshot.go) | 视频快照裁剪，无独立网络端点 |

## 输入 / 输出 / 边界

- **输入**：BV、UID、context，以及 B站 HTTP 响应。
- **输出**：`Video`、`CreatorProfile`、`CreatorVideo`、`*source.Error`；视频 `Raw` 为适配器快照，作者 `Raw` 保留原始 `data`；不输出过滤规则版本。存储契约见 [数据库设计](../../../../docs/database.md#payload-保存什么)。
- **它不认识**：数据库、调度器、8181/8182 服务；Cookie 只来自 `BHT_BILI_COOKIE`。

## 关键约束

- 端点固定，不接受调用方 URL；不跟随重定向；单视频和资料不自动重试，公开视频发现由 `list_page.go` 分类有限重试。
- 响应体上限 2 MiB；HTTP 200 与业务 `code=0` 分开检查。
- 视频当前使用 `videoSnapshotV1()`，只删除顶层 `ugc_season`，保留所有分 P、统计、字幕、未知字段和大整数；不递归过滤。过滤版本只在代码中管理，测试见 [snapshot_test.go](snapshot_test.go)。裁剪发生在响应读取后，不减少上游网络流量，也不改变响应体上限。

作者视频分页入口为 [list_page.go](list_page.go) 的 `GetCreatorVideoPage`：内部遍历目录和成员，输出扁平视频页；不带 Cookie、不请求空间投稿列表，不把预览当作完整成员。测试见 [list_page_test.go](list_page_test.go)，策略见 [作者视频设计](../../../../docs/video-discovery.md#retry-policy)。旧 `list.go` 仅保留兼容能力，新链路不使用其 Cookie 回退。
