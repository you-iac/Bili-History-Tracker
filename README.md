# Bili History Tracker

视频历史追踪项目。服务端按 BV 查询视频元数据、按 UID 查询 UP 主资料，并把成功观测写入 SQLite；
8182 端口提供观察台，可添加 UP 主或视频作为跟踪目标、设置查询间隔（默认 3 小时）、查看查询日志与入库详情。
作者视频列表融合本地记录与平台发现，排除已关注项并分页展示；默认不添加，支持逐项选择和跨页一键添加全部。

需要 Go 1.26 或更高版本。

```powershell
cd backend
go run ./cmd/server
```

默认监听：

- 查询 API：`127.0.0.1:8181`
- 观察台：`http://127.0.0.1:8182/`

后台不设登录和权限，默认只监听回环地址。

## 浏览器客户端与本地 Agent

客户端统一放在 [client/](client/README.md)，共同需求与设计维护在该目录的 README；[client/frontend/](client/frontend/README.md) 放浏览器界面，[client/agent/](client/agent/README.md) 放本地后台。启动 Agent 后，在浏览器打开它提供的本地页面；Agent 同源提供网页和 API，处理配置、Cookie、同步、发现及后续下载，关闭网页后仍可继续后台任务。

当前已实现独立的 Cookie 配置与 UP 主视频 ID 扫描：验证自己的 B站 Cookie，输入 UID 或空间链接，分页获取当前可见投稿的全部 BV 号，支持停止/继续、复制和 TXT/JSON 导出。Windows Cookie 由当前账户加密保存，扫描结果逐页落盘；普通网页不能直接读取 B站 Cookie，本期采用手动导入。

```powershell
cd client
go run ./agent/cmd/agent
```

打开终端显示的完整本地链接（默认 `127.0.0.1:8183`）。已支持本地关注 UP/视频、保存服务器8182地址、提交目标及读取资料/指标/版本，关注与缓存重启保留，取消不影响公共采集。远程 backend 需一起更新；操作见 [客户端说明](client/README.md#当前实现与启动)，协议和验证见 [关注接入](docs/client-library.md)。下载、完整数据管理和云盘仍在 [PLAN](PLAN.md) 中。

## 观察台

打开 `http://127.0.0.1:8182/`：

- 信息总览：已记录数量、近 7 日更新、需关注问题、排行与累计趋势
- 视频库 / UP 主库：搜索、排序、关注筛选、分页
- 动态与提醒：内容版本变化、采集异常与恢复
- 对象详情：指标趋势、自记录增量、版本时间线与字段对比、业务快照
- 关注管理：添加视频或 UP 主、修改间隔、立即刷新、停止关注
- 采集记录：最近 500 条查询日志，筛选与分页

常规内容页只读本地；作者的可添加视频列表会只读发现平台视频，列表本身不保存内容。勾选提交或添加全部才采集并关注视频，作者资料关注不会自动关注视频。页面职责、状态判定和统计口径见 [观察台设计](docs/dashboard.md)。

哔哩哔哩适配器内部遍历公开可见的目录及成员，向服务器只返回作者视频页。发现不携带 Cookie，不回退需要登录的空间列表；这只覆盖服务器当前可获取的范围，不能保证全部投稿。详见 [作者视频设计](docs/video-discovery.md)。

## 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `BHT_ADDR` | `127.0.0.1:8181` | 查询 API 监听地址 |
| `BHT_ADMIN_ADDR` | `127.0.0.1:8182` | 观察台监听地址 |
| `BHT_DB` | `data/bht.db` | SQLite 文件路径 |
| `BHT_QUERY_LOG` | 数据库同目录的 `query.log` | 查询日志，JSON Lines |
| `BHT_BILI_COOKIE` | 空 | 旧查询能力的兼容配置；新的作者视频发现明确不携带此凭据 |

数据库和日志位于 `backend/data/`，已被 `.gitignore` 忽略。

## 数据模型

五张内容表（`creators`、`videos`、`creator_versions`、`video_versions`、`metric_samples`）加一张调度表 `tracking_targets`。
当前表保存列表与调度需要的字段和版本指针，版本表保存适配器准备的业务快照：B站视频只过滤顶层 `ugc_season` 合集详情，保留当前视频的所有分 P 和其他字段；作者资料保留完整上游 `data`。过滤规则版本只在适配器代码中管理，不入库。内容指纹只覆盖标题、简介、封面、分P 等稳定字段，
播放量和粉丝数变化不会制造新版本。设计、迁移与读取路径见 [数据库设计](docs/database.md)。

数据库打开时自动迁移到 schema 4：0003 删除快照过滤版本列，0004 为每个关注目标保存最近采集状态和失败恢复信息，供观察台展示，并用已有成功观测回填旧目标。已有 payload 不自动裁剪，内容未变时仍读取旧快照；首次保存或内容变化后才写入裁剪后的新快照。

## 查询 API

```powershell
curl.exe http://127.0.0.1:8181/healthz
curl.exe http://127.0.0.1:8181/api/v1/platforms/bilibili/videos/BV17x411w7KC
curl.exe http://127.0.0.1:8181/api/v1/platforms/bilibili/creators/122541
```

视频查询仅接受完整 BV 号，区分大小写，不接受视频链接或 av 号。返回标题、简介、封面地址、UP 主、发布时间、时长、播放量、来源地址和观测时间；不请求播放地址或下载媒体。

UP 主资料查询接受正整数 UID（不带前导零，最大值为 `9223372036854775807`），不接受个人空间链接。返回 UID、昵称、头像地址、个人签名（`description`）、空间地址、粉丝数、来源地址和观测时间；粉丝数未知时为 `null`。查询资料不会触发该 UP 主的投稿列表获取。

B站网页端点（`/x/web-interface/view`、`/x/web-interface/card`、合集接口）属于实验性接入，不是有稳定性承诺的开放平台 API。适配器不使用登录 Cookie（除非显式配置），视频和资料查询不自动重试；作者视频发现仅对临时错误有限重试，权限和风控错误停止，不绕过访问限制。

## 验证

```powershell
cd backend
go test ./...
go vet ./...
node --test internal/admin/chart_test.cjs internal/admin/discovery_test.cjs
go build -o bin/server.exe ./cmd/server
```

修改代码后需重启服务才能生效；查询结果已持久化，历史内容在重启后保留。元数据查询成功也不代表视频可以播放；查询不到不代表视频已被删除。

执行链路、模块导航和后台调度说明见 [架构说明](docs/architecture.md)。模块注释与文档维护约定见 [AGENTS.md](AGENTS.md)。
