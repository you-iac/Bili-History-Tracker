# 浏览器界面

状态：已实现 Cookie 配置、账号摘要、UP 主投稿扫描、分页清单与 TXT/JSON 导出。界面与 Agent 共用 [客户端需求与设计](../README.md)，模块导航见 [docs/architecture.md](../../docs/architecture.md#module-navigation)，接口与凭据边界见 [技术说明](../../docs/client-discovery.md)。

主要职责：

- [index.html](index.html) 与 [app.css](app.css)：账号配置、扫描进度、结果列表和响应式布局，不提供尚未实现功能的入口。
- [app.js](app.js)：同源会话交换、Cookie 提交与清除、扫描轮询、取消/继续、分页和导出；敏感值不写入浏览器存储。
- [view.mjs](view.mjs)：状态、进度与北京时间显示，未知总量不伪造完成；[view.test.mjs](view.test.mjs) 覆盖关键边界。
- 通过同源本地 HTTP API 向 [Agent](../agent/README.md) 提交配置和任务操作，读取校验结果与持久化状态；远端服务器和平台请求由 Agent 执行。
- 显示服务器离线、Agent 断连、凭据失效和任务失败等状态，刷新或关闭页面不取消 Agent 已接收的任务。

使用原生 HTML/CSS/JavaScript，无运行时外部依赖或 CDN。[assets.go](assets.go) 通过 `go:embed` 将资源编入 Agent；修改资源后重新运行或构建 Go 程序。在 `client/` 执行 `npm run lint` 和 `npm test` 检查前端，无需安装依赖；已安装 pnpm 时也可使用 `pnpm lint`、`pnpm test`。

下一步是本地私人 UP/视频关注库，按标识合并服务器公共历史；本地增删关注，必要时申请公共跟踪，取消不全局停采。多人各自用 Agent，服务器无需个人账户；视频下载本地执行，评论和弹幕后续加入。页面与边界见 [共同需求](../README.md#页面与操作) 和 [协作设计](../../docs/client-server-architecture.md)。

规划提供“数据管理”：档案与占用、分类清理、导入导出、备份恢复；未来展示用户自选云盘的连接、自动计划、进度、历史版本和恢复控制。敏感凭据与媒体默认不随档案备份，恢复先预览，见 [数据管理设计](../../docs/client-data-management.md)。这些页面尚未实现。
