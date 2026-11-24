# 点赞、投币、收藏视频的查看与自动关注

- 创建时间：2026-11-24 20:30（Asia/Shanghai）。
- 状态：实施中；增量发现与互动查询已完成，TTL 重查、失败退避和自动关注待实现。
- 依赖：本地 Cookie、`library.Follow`/`Submit`、后台循环任务。

## 目标

1. 查看当前账号点赞、投币、收藏的视频；
2. 与本地关注/记录去重，支持手动关注；
3. 设置开启自动关注后，后台循环增量发现新历史，并对历史条目按 TTL 重查互动状态，自动关注有互动的视频；
4. 复用 `Follow`/`Submit` 推送服务器。

## 数据来源（已实测）

| 来源 | 接口 | 说明 |
|---|---|---|
| 收藏 | `/x/v3/fav/folder/created/list-all` + `/x/v3/fav/resource/list` | 已确认可用，分页拉全部收藏夹内容 |
| 点赞 | `/x/web-interface/archive/has/like?bvid=` | 单视频返回 0/1 |
| 投币 | `/x/web-interface/archive/coins?bvid=` | 返回 `data.multiply`，0/1/2 表示用户投币数 |

- 点赞/投币没有公开的完整列表接口，只能对已知历史视频逐条查询；
- 转发暂不支持；
- 所有请求需要登录态 Cookie。

## 存储模型

历史按 `bvid + viewed_at` 保留每次观看：

- 同一视频多次观看保留多条记录；
- 互动状态按 bvid 共享，查询一次写回该视频的所有观看记录；
- 列表展示按 bvid 聚合，显示“看过 N 次”和最近观看时间；
- 自动关注按 bvid 去重，一个视频只关注一次。

每条历史记录保存：

```text
bvid / title / cover / author_id / author_name
viewed_at / progress / duration
like / coin / favorite
interaction_checked_at
interaction_failures / interaction_next_attempt
```

## 增量发现

```text
每轮同步
  → 拉历史最新一页（30 条）
  → 与本地按 bvid + viewed_at 去重
  → 新观看记录：立即查互动
  → 已存在记录：不做全量重查，按 TTL/退避处理
  → 当前页大量已存在且时间旧 → 停止继续翻页
```

停止条件：

- 本页新增观看记录低于阈值（如 0–2 条）；或
- 本页最旧 `viewed_at` 超过上次同步位置。

## 互动重查（TTL + 失败退避）

- 最近 3 天的记录：TTL 6 小时；
- 3–30 天的记录：TTL 24 小时；
- 按 bvid 查询一次，写回该视频所有观看记录；
- 每轮最多查 60 个 bvid，按 `interaction_checked_at` 最旧优先；
- 跳过 `interaction_next_attempt > now` 的记录。

成功：

```text
interaction_failures = 0
interaction_next_attempt = 0
interaction_checked_at = now
```

失败：

```text
interaction_failures++
interaction_next_attempt = now + min(5min * 2^(failures-1), 6h)
```

这样失败不会每轮重打，最多退避到 6 小时。

## 自动关注

```text
有互动（like=1 或 coin>0 或 favorite=true）
  且未关注
  → library.Follow（本地 + 服务器 Submit）
  → 打 auto 标记
```

- 按 bvid 去重；
- 每轮上限 20 条；
- 失败进待重试队列；
- 自动关注可批量撤销；
- 关闭开关后停止。

## 设置

- 自动关注开关；
- 来源勾选：点赞 / 投币 / 收藏；
- 检查频率：30 分钟 / 1 小时 / 6 小时；
- 只自动关注最近 N 天（默认 30 天）。

## 边界

- 互动数据只存本地；
- Cookie 不出本机；
- 推送服务器复用 `Follow`/`Submit`，不新增接口；
- 转发暂不做；
- 不自动取消关注；
- 风控/限流退避。

## 验收

- 同一视频多次观看保留多条记录，列表显示“看过 N 次”；
- 历史增量发现不会每次全量翻页；
- 新增记录立即查互动，旧记录按 TTL 重查；
- 失败按指数退避，不每轮重试；
- 有互动且未关注 → 自动关注并推送；
- 转发不参与；
- 去重、预算、失败队列生效；
- 测试通过。

## 实施顺序

1. 本文档；
2. 历史增量发现与停止条件（已完成）；
3. 点赞/投币查询 + 收藏集合（已完成）；
4. 存储模型改为 bvid + viewed_at，聚合展示；
5. TTL 重查与失败退避；
6. 自动关注循环与设置；
7. 验收。
