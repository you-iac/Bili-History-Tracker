# Agent 架构

Agent 是运行在本机的 Go 程序：提供网页、本地 UP/视频关注库、服务器连接与资料读取，并保留 Cookie 投稿扫描。

```text
+------------------------------+
| browser: frontend            |
+------------------------------+
               | local HTTP + session
               v
+------------------------------+
| Agent: library / scanner     |
+------------------------------+
               | HTTP(S): read / submit
               v
+------------------------------+
| 8182: records / tracking     |
+------------------------------+
```

结果原路返回。另一路由 `scanner → bilibili` 使用本地 Cookie 获取投稿，不与8182共享 Cookie。

| 模块 | 职责 |
|---|---|
| [main](cmd/agent/main.go) | 组装回环服务并自动打开已认证网页，默认8183 |
| [httpapi](internal/httpapi/server.go)、[关注接口](internal/httpapi/library.go) | 网页与25个API，会话、来源、CSRF保护 |
| [library](internal/library/service.go) | 本地关注/公开搜索、总览缓存、自动提交与退避队列，见 [pending.go](internal/library/pending.go) |
| [remote](internal/remote/client.go) | 按固定8182路径查询/提交，不转发平台 Cookie或本地会话 |
| [scanner](internal/scanner/service.go)、[bilibili](internal/bilibili/client.go) | Cookie验证、投稿分页、取消与检查点恢复 |
| [localstate](internal/localstate/store.go) | 状态原子保存与 [目录迁移](internal/localstate/directory.go)，初始目录保留位置指向 |

关注先保存本地，再自动提交服务器；失败标为待提交并在Agent后台退避重试，不删除关注。取消只影响本地，服务器继续公共跟踪。资料按需读取并缓存，断网显示缓存时间与错误；Agent不运行周期跟踪。

网页通过 [go:embed](../frontend/assets.go) 发布，无需前端开发服务。服务器提供既有8182库/详情JSON和目标提交接口时，在网页保存地址并测试，即可使用远程资料；本轮无需修改backend。Cookie仅是本地投稿发现的前置条件。自动启动/认证及历史图片URL缓存见 [启动与图片](../../docs/client-launch-media.md)。

## 目标扩展（待实施）

下载、评论/弹幕、档案管理/完整备份、自选云盘、事件增量同步，以及公共服务完整准入均留待后续。当前一个实例使用一份本地关注和配置，不提供共享实例多用户隔离。

实现与限制见 [关注库技术说明](../../docs/client-library.md)，整体目标见 [协作设计](../../docs/client-server-architecture.md)，私人数据规划见 [数据管理](../../docs/client-data-management.md)。

浏览器侧栏包含总览、卡片库、独立详情、投稿发现和设置；详情趋势/版本与总览分页的数据来源见 [库与详情](../../docs/client-catalog.md)。工作台操作、队列和目录指向见 [工作台说明](../../docs/client-workspace.md)。

## API 接口

默认 `http://127.0.0.1:8183`。除建立会话外均需本地会话；写操作还需同源 JSON 与 `X-CSRF-Token`。

| 方法与路径 | 用途 / 参数 |
|---|---|
| `POST /api/session` | `{"token":"启动密钥"}` 建立会话并返回CSRF token |
| `GET /api/session` | 当前会话CSRF token |
| `GET /api/config` | Cookie配置与账号摘要，不返回Cookie |
| `PUT /api/config/cookie` | `{"cookie":"..."}` 验证并保存 |
| `POST /api/config/validate` | 验证已有Cookie |
| `DELETE /api/config/cookie` | 清除Cookie，保留其他数据 |
| `POST /api/scan` | `creator_id`、`resume`，扫描或继续 |
| `GET /api/scan?offset=0&limit=50` | 扫描进度与分页结果 |
| `POST /api/scan/cancel` | 停止扫描，保留结果 |
| `GET /api/scan/export?format=txt` | TXT或JSON导出扫描 |
| `GET /api/remote/connection` | 已保存的服务器地址 |
| `PUT /api/remote/connection` | `{"server_url":"http://...:8182"}`，空字符串断开；切服清缓存保留关注 |
| `POST /api/remote/test` | 测试已保存服务器的JSON能力 |
| `GET /api/overview` | 本地关注/待提交数量、服务器地址与最近公开总览 |
| `POST /api/overview/refresh` | 用既有UP/视频库接口聚合总数与首页列表并缓存 |
| `GET /api/remote/catalog?kind=creator&q=&page=1` | 只读公开库搜索，按本地列表补个人关注标记 |
| `GET /api/images/{kind}/{id}?seq=1` | 从既有快照解析头像/封面URL并按版本缓存；返回url/seq/available/cached，不代理图片文件 |
| `GET /api/settings` | 活动/启动目录与状态文件占用 |
| `PUT /api/settings/directory` | `data_directory` 绝对路径，复制到空目录并持久化切换 |
| `GET /api/follows?kind=creator&page=1&q=` | 本地UP/视频关注，分页20条及分类总数 |
| `POST /api/follows` | `{"kind":"creator或video","id":"UID/BV/标准链接"}` 保存本地关注并自动提交；失败返回待提交状态 |
| `DELETE /api/follows/{kind}/{id}` | 仅取消本地关注，不停服务器采集 |
| `POST /api/follows/{kind}/{id}/refresh` | 核对公共状态，读取并保存最近资料/版本/指标 |
| `POST /api/follows/{kind}/{id}/submit` | 提交已保存目标到8182，服务器确认后返回 |
| `GET /api/library/{kind}/{id}?days=30&page=1&seq=1` | 读取公开详情、7/30/90/all趋势、版本页与快照；附带个人关注/来源和匹配的30日离线缓存状态，不自动提交跟踪 |

扫描后台执行，网页关闭不取消；关注刷新/提交绑定当前请求，失败可显式重试；待提交申请由后台退避处理。完整行为见 [本地关注实现](../../docs/client-library.md)，原扫描协议见 [发现说明](../../docs/client-discovery.md#本地-api-与会话)。
