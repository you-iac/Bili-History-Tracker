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
| [main](cmd/agent/main.go) | 组装并启动回环服务，默认8183 |
| [httpapi](internal/httpapi/server.go)、[关注接口](internal/httpapi/library.go) | 网页与19个API，会话、来源、CSRF保护 |
| [library](internal/library/service.go) | 本地关注增删/分页/搜索、连接配置、最近公共资料与错误 |
| [remote](internal/remote/client.go) | 按固定8182路径查询/提交，不转发平台 Cookie或本地会话 |
| [scanner](internal/scanner/service.go)、[bilibili](internal/bilibili/client.go) | Cookie验证、投稿分页、取消与检查点恢复 |
| [localstate](internal/localstate/store.go) | `library.json`、扫描检查点与受保护Cookie保存 |

关注先保存本地，再按用户选择提交服务器；失败不删除关注。取消只影响本地，服务器继续公共跟踪。资料按需读取并缓存，断网显示缓存时间与错误；Agent不运行周期跟踪。

网页通过 [go:embed](../frontend/assets.go) 发布，无需前端开发服务。更新服务器 backend 后，在网页保存8182地址并测试，即可使用远程资料；Cookie仅是本地投稿发现的前置条件。

## 目标扩展（待实施）

下载、评论/弹幕、档案管理/完整备份、自选云盘、事件增量同步，以及公共服务完整准入均留待后续。当前一个实例使用一份本地关注和配置，不提供共享实例多用户隔离。

实现与限制见 [关注库技术说明](../../docs/client-library.md)，整体目标见 [协作设计](../../docs/client-server-architecture.md)，私人数据规划见 [数据管理](../../docs/client-data-management.md)。

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
| `GET /api/follows?kind=creator&page=1&q=` | 本地UP/视频关注，分页20条及分类总数 |
| `POST /api/follows` | `{"kind":"creator或video","id":"UID/BV/标准链接"}` 仅保存本地关注 |
| `DELETE /api/follows/{kind}/{id}` | 仅取消本地关注，不停服务器采集 |
| `POST /api/follows/{kind}/{id}/refresh` | 核对公共状态，读取并保存最近资料/版本/指标 |
| `POST /api/follows/{kind}/{id}/submit` | 提交已保存目标到8182，服务器确认后返回 |
| `GET /api/library/{kind}/{id}?page=1&seq=1` | 读取已关注目标详情、版本页与可选历史快照，不自动提交跟踪 |

扫描后台执行，网页关闭不取消；关注刷新/提交绑定当前请求，失败可显式重试。完整行为见 [本地关注实现](../../docs/client-library.md)，原扫描协议见 [发现说明](../../docs/client-discovery.md#本地-api-与会话)。
