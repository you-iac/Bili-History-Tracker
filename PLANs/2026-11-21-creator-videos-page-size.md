# UP 详情视频区按卡片密度调整每页数量

- 创建时间：2026-11-21 20:45（Asia/Shanghai）。
- 状态：待实施；本项先完成文档与参数设计，不修改业务代码。

## 目标

UP 详情的“已关注视频 / 未关注投稿”列表目前固定每页 20 条，卡片密度只改变 CSS，紧凑模式最后一行仍会空缺。改为每页数量随密度变化，让紧凑模式更满、宽松模式不拥挤。

## 现状

- `client/agent/internal/library/catalog.go` 的 `ListCatalog` 和 `ScanCatalog` 固定 `Limit: 20`，分页公式为 `(len+19)/20`。
- `client/agent/internal/httpapi/library.go` 的 `creatorVideos` 只解析 `page`，没有 `limit`。
- `client/frontend/creator-videos.js` 的 `density()` 只设置 `data-size` 和 `bht.card-size`，不改变每页条数；请求参数是 `scope/sort/q/page`。
- 卡片密度对应列数不同：紧凑列多、宽松列少，固定 20 条会让紧凑模式最后一行空、宽松模式行数过多。

## 方案

### 后端

`ListCatalog` / `ScanCatalog` 增加有界 `limit`：

```text
limit: 12..48，默认 20
```

- 非法值（非数字、越界）由 HTTP 层返回 400；
- 低于 12 或高于 48 按 400 处理，不静默钳制；
- `Page.Limit` / `PublicPage.Limit` 返回实际值；
- 分页公式改为 `(len+limit-1)/limit`，起始位置改为 `(page-1)*limit`。

### HTTP

`GET /api/creator-videos/{id}` 和 `GET /api/follows` 增加 `limit` 查询参数，默认 20；解析失败或越界返回 400。

### 前端

密度映射：

| 密度 | 每页条数 |
|---|---|
| compact | 30 |
| standard | 20 |
| large | 12 |

- `density(value)` 同时记录 `limit`；
- 切换密度时 `page=1` 并重新 `load()`；
- 请求 URL 加 `limit`；
- 分页文案和按钮使用 `result.limit`；
- 偏好仍存 `bht.card-size`。

## 边界

- 只影响 UP 详情视频区的每页条数；
- 不改变排序、筛选、扫描范围；
- 服务器公共 API 的 `/videos`、`/creators` 仍固定 18，不在本项范围；
- 密度只影响布局和分页，不影响关注/扫描逻辑；
- 手机单列布局继续可用。

## 验收

- 紧凑模式每页 30 条，标准 20 条，宽松 12 条；
- 页码、总数、上一页/下一页按钮随 limit 正确变化；
- 切换密度后回到第 1 页；
- 非法 `limit`（`0`、`abc`、`100`）返回 400；
- 关注本页按钮数量仍按当前页未关注数计算；
- `go test ./...`、`node --test` 通过。

## 实施顺序

1. 本文档；
2. 后端 `limit` 与测试；
3. HTTP 参数解析与测试；
4. 前端密度映射与请求；
5. 手动验收三种密度。
