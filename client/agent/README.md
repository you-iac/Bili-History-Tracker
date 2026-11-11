# 本地 Agent

状态：已实现本地会话、Cookie 配置和 UP 主投稿 ID 扫描，其他客户端能力待实施。入口为 [cmd/agent/main.go](cmd/agent/main.go)；在 `client/` 执行 `go run ./agent/cmd/agent`，打开终端给出的完整本地链接。浏览器界面与后台共用 [客户端需求与设计](../README.md)，模块导航见 [docs/architecture.md](../../docs/architecture.md#module-navigation)，实现与 API 见 [技术说明](../../docs/client-discovery.md)。

Agent 运行在用户设备上，托管 [client/frontend/](../frontend/README.md) 内嵌资源与同源本地 HTTP API，不依赖现有 `backend/` 服务。目录路径以仓库根目录为基准；需求、页面操作和验收统一维护在上级文档。

主要职责：

- [httpapi](internal/httpapi/server.go)：本地会话、Host/Origin/CSRF 检查、配置与扫描 API，配置读取只返回摘要。
- [bilibili](internal/bilibili/client.go)：Cookie 登录验证、WBI 签名和投稿分页；仅请求固定平台地址，禁止重定向，不回退公开合集。
- [scanner](internal/scanner/service.go)：独立后台扫描、跨页去重、完整性判断、取消与恢复；网页关闭不取消任务。
- [localstate](internal/localstate/store.go)：逐页原子保存结果；Windows 使用当前用户 DPAPI 加密 Cookie，其他平台暂只在内存保留。

服务器记录同步、跟踪交接、自动规则、下载及评论尚未实现。当前仅保留最近一次扫描，开始新扫描会替换旧任务；恢复时重新验证平台账号，账号不同则要求切回原账号或重新扫描。
