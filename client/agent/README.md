# Agent 架构

Agent 是运行在用户电脑上的 Go 程序：提供网页、管理 Cookie、后台获取 UP 主视频 ID。

下图和现有 API 描述当前已实现部分；目标是接入远程历史与关注管理、本地视频下载，见下方“目标扩展”。

```text
+------------------------------+
| 浏览器：frontend             |
+------------------------------+
               | 本地 HTTP：配置和操作
               v
+------------------------------+
| Agent：接口、扫描、本地存储  |
+------------------------------+
               | HTTPS：携带 Cookie
               v
+------------------------------+
| B站：返回账号和投稿信息      |
+------------------------------+
```

Agent 内部分工：

| 入口 / 模块 | 职责 |
|---|---|
| [main](cmd/agent/main.go) | 组装模块，启动本地服务，默认端口 8183 |
| [httpapi](internal/httpapi/server.go) | 提供网页和接口，校验会话、来源及写操作 |
| [scanner](internal/scanner/service.go) | 管理 Cookie 和扫描任务，分页去重、停止和继续 |
| [bilibili](internal/bilibili/client.go) | 封装 B站请求和签名，验证账号、读取投稿 |
| [localstate](internal/localstate/store.go) | 保存 Cookie、扫描结果和下一页位置 |

网页通过 [go:embed](../frontend/assets.go) 打包进 Agent，无需单独启动前端服务。

输入 UID 后，`httpapi` 交给 `scanner`；`scanner` 调用 `bilibili` 逐页取数据，再用 `localstate` 保存。网页定时查询进度，复制或导出已获取的 BV 号。

一次只扫描一个 UP，保留最近一次结果。关闭网页不停止扫描；退出 Agent 后本地任务停止，重启后可继续。Windows Cookie 使用当前用户加密保存，其他系统暂只存在内存。

当前独立运行，不连接项目 `backend/`；同步、自动规则、下载和评论尚未实现。

## 目标扩展（待实施）

- 远程服务器持久化跟踪 UP 和视频，提供资料、版本、粉丝与播放量变化，以及关注增删。
- Agent 增加远程客户端和两个关注库；按需搜索发现、提交关注，本地直接下载当前可用视频。
- Agent 不运行本地长期跟踪规则，保留配置、缓存、待确认操作和下载进度；评论和弹幕后续加入。
- 建议本地不另建账号体系，使用远程工作区设备凭据；多人时在服务器登录并隔离关注。本地会话及 B站 Cookie 仍分别管理。

完整模块与 API 草案见 [远程协作设计](../../docs/client-server-architecture.md)。这些扩展尚未实现，下面仍是当前可调用的 10 个接口。

## API 接口

默认地址：`http://127.0.0.1:8183`。浏览器通过这些接口操作 Agent，实现在 [server.go](internal/httpapi/server.go)。

| 方法与路径 | 用途 / 参数 |
|---|---|
| `POST /api/session` | 传入 `{"token":"启动密钥"}`，建立本地会话并返回 CSRF token |
| `GET /api/session` | 读取当前会话的 CSRF token |
| `GET /api/config` | 查看 Cookie 配置状态和账号摘要，不返回 Cookie 原文 |
| `PUT /api/config/cookie` | 传入 `{"cookie":"..."}`，验证并保存 B站 Cookie |
| `POST /api/config/validate` | 验证已保存的 Cookie |
| `DELETE /api/config/cookie` | 清除 Cookie，保留扫描结果 |
| `POST /api/scan` | 传入 `{"creator_id":"123","resume":false}` 开始扫描；`resume:true` 继续上次扫描 |
| `GET /api/scan?offset=0&limit=50` | 查询任务进度和分页结果，`limit` 为 1–100 |
| `POST /api/scan/cancel` | 停止扫描，保留已获取结果 |
| `GET /api/scan/export?format=txt` | 导出全部已获取 ID；`format=json` 导出任务及视频信息 |

`POST /api/scan` 提交后立即返回，扫描在后台继续；网页定时调用 `GET /api/scan` 更新进度。

建立会话使用 Agent 启动密钥，其余 API 需要本地会话；写操作还需同源 JSON 和 `X-CSRF-Token`。启动密钥与 B站 Cookie 分开使用，详细约定见 [本地 API 与会话](../../docs/client-discovery.md#本地-api-与会话)。

启动见 [客户端说明](../README.md#当前实现与启动)，完整接口与边界见 [技术说明](../../docs/client-discovery.md)，模块与测试见 [架构导航](../../docs/architecture.md#module-navigation)。
