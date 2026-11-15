# 浏览器界面

状态：已实现侧栏总览、可调大小卡片库、独立详情趋势/版本对比、总览分页、公开搜索、自动关注及设置页（服务器/目录/Cookie），保留投稿发现与导出。总览只读服务器既有UP/视频库，不展示需要新增后端接口的全局跟踪数和近期版本动态。本轮不改backend。共同需求见 [客户端说明](../README.md)，实现与验证见 [工作台说明](../../docs/client-workspace.md) 和 [关注接入](../../docs/client-library.md)。

主要职责：

- [index.html](index.html) 与 [app.css](app.css)：账号配置、扫描进度、结果列表和响应式布局，不提供尚未实现功能的入口。
- [app.js](app.js)：同源会话交换、Cookie 提交与清除、扫描轮询、取消/继续、分页和导出；敏感值不写入浏览器存储。
- [navigation.js](navigation.js)：白名单路由、侧栏与移动导航；页面切换不终止后台任务。
- [library.js](library.js)：总览、公开库搜索、自动关注、设置与分页总览；详情使用独立路由；文本使用 `textContent`，所有远程请求经 Agent。
- [cards.js](cards.js)、[catalog.css](catalog.css)：响应式卡片与三档大小。
- [detail.js](detail.js)、[trend.mjs](trend.mjs)：详情路由、UTC真实日采样与版本对比，见 [库与详情](../../docs/client-catalog.md) 及 [趋势测试](trend.test.mjs)。
- [view.mjs](view.mjs)：状态、进度与北京时间显示，未知总量不伪造完成；[view.test.mjs](view.test.mjs) 覆盖关键边界。
- 通过同源本地 HTTP API 向 [Agent](../agent/README.md) 提交配置和任务操作，读取校验结果与持久化状态；远端服务器和平台请求由 Agent 执行。
- 显示服务器离线、Agent 断连、凭据失效和任务失败等状态，刷新或关闭页面不取消 Agent 已接收的任务。

使用原生 HTML/CSS/JavaScript，无运行时外部依赖或 CDN。[assets.go](assets.go) 通过 `go:embed` 将资源编入 Agent；修改资源后重新运行或构建 Go 程序。在 `client/` 执行 `npm run lint` 和 `npm test` 检查前端，无需安装依赖；已安装 pnpm 时也可使用 `pnpm lint`、`pnpm test`。

当前库关注自动提交并由Agent后台重试，公开搜索本身不提交，取消不全局停采；扫描结果可逐项关注，UP 可跳转原扫描入口。下载、评论/弹幕和完整数据管理仍待实现，规划见 [共同需求](../README.md#页面与操作) 和 [协作设计](../../docs/client-server-architecture.md)。

规划提供“数据管理”：档案与占用、分类清理、导入导出、备份恢复；未来展示用户自选云盘的连接、自动计划、进度、历史版本和恢复控制。敏感凭据与媒体默认不随档案备份，恢复先预览，见 [数据管理设计](../../docs/client-data-management.md)。已提供目录迁移和占用查看，完整档案、备份和云盘页面尚未实现。
