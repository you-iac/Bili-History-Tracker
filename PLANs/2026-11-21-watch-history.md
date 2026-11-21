# 观看历史同步与手动关注

- 创建时间：2026-11-21 22:10（Asia/Shanghai）。
- 状态：待实施；本项先完成文档与接口设计，不修改业务代码。
- 与“特别关注与自动跟踪新视频”是独立功能：本项只做历史拉取、展示和显式关注，不做自动关注。

## 目标

用 Agent 本地保存的 B站 Cookie 拉取自己的观看历史（最多最近 30 天），在本地缓存并展示视频与 UP 主列表；用户从历史中选择关注，复用现有单条关注流程。历史只存本地，不上传服务器。

## 现状

- Agent 已有 B站 Cookie（`/api/config/cookie`），[bilibili.Client](../client/agent/internal/bilibili/client.go) 支持带 Cookie 的 wbi 请求，`Videos` 已使用 `space/wbi/arc/search`。
- 观看历史接口尚未接入；前端没有历史页面。
- 关注入口目前只有：手动输入 BV/UID、UP 详情未关注投稿。

## 数据来源

B站接口：

```text
GET /x/web-interface/history/cursor
  ?max=<cursor>&view_at=<cursor>&business=archive
```

- 需要登录态 Cookie；
- 需要 wbi 签名；
- 分页游标为 `max`/`view_at`；
- 每条返回含 `bvid`、`title`、`cover`、`author_mid`、`author_name`、`view_at`、`progress`、`duration`。

## 本地存储

- 历史记录只存在 Agent 本地（JSON 或 SQLite，实施时定）；
- 去重键：`bvid + view_at`，同一视频多次观看保留最近一次；
- 最多保留 30 天，超过清理；
- 同步时按游标拉取，最多到 30 天前，按请求预算分批，失败保留游标下次继续。

## 前端

新增“观看历史”入口：

- 范围筛选：最近 3 / 7 / 30 天，只过滤本地，不重新拉取；
- 两个标签：视频 / UP 主；
- 视频卡片复用现有卡片，标记：
  - 已关注（本地 follows）
  - 已记录未关注（本地 videos/creators）
  - 未记录
- UP 主按 `author_mid` 聚合：最近观看时间、观看视频数、是否已关注；
- 支持单条关注；批量关注沿用“关注本页”的串行单条实现。

## 接口

Agent：

| 接口 | 作用 |
|---|---|
| `POST /api/history/sync` | 拉取最近 30 天历史，返回新增/更新数量 |
| `GET /api/history/videos?days=3\|7\|30&page=&limit=` | 本地历史视频分页 |
| `GET /api/history/creators?days=3\|7\|30&page=&limit=` | 本地历史 UP 聚合分页 |

- `days` 只过滤本地；
- `limit` 取 12–48，默认 20；
- 非法参数返回 400。

## 边界

- 历史只存本地，不上传服务器；
- Cookie 不进入日志或错误消息；
- 同步失败保留已有缓存，返回可读错误；
- 风控/限流退避，不无限重试；
- 历史里的关注仍是显式操作，不自动关注。

## 验收

- 同步后能看到最近 3/7/30 天的视频与 UP；
- 已关注/已记录/未记录标记正确；
- 关注历史视频后，本地关注和服务器申请正常；
- 超过 30 天的本地历史被清理；
- `days`/`limit` 非法返回 400；
- `go test ./...`、前端 `node --test` 通过。

## 实施顺序

1. 本文档；
2. `bilibili.Client` 增加历史接口与 wbi 签名；
3. 本地历史存储、去重、30 天清理；
4. Agent HTTP 接口；
5. 前端历史页面与关注；
6. 手动验收。
