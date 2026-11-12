# Agent 架构

Agent 是运行在用户电脑上的 Go 程序：提供网页、管理 Cookie、后台获取 UP 主视频 ID。

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

启动见 [客户端说明](../README.md#当前实现与启动)，完整接口与边界见 [技术说明](../../docs/client-discovery.md)，模块与测试见 [架构导航](../../docs/architecture.md#module-navigation)。
