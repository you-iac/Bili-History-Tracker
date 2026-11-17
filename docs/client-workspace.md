# 客户端工作台与关注操作

状态：已实现。当前仍为 Go Agent + 浏览器，Qt 重构继续保留未来计划；本次提供侧栏工作台、公开搜索、关注自动提交与工作目录设置。自动启动认证与头像/封面展示见 [启动与图片](client-launch-media.md)。卡片大小、独立详情、趋势/版本对比和总览分页见 [库与详情](client-catalog.md)。本说明保留基础工作台行为；本版新增目录排序、详情内投稿和摘要回填见 [库与详情](client-catalog.md)，新增公开目录统计需要对应 backend 支持。

## 页面与来源

| 侧栏 | 内容与操作 |
|---|---|
| 信息总览 | 服务器既有UP/视频库的总条目数与完整分页记录（当前18条/页），可进入完整库，单独展示本地关注与待提交数量；不展示全局跟踪数或近期版本动态 |
| UP主库 | 默认显示本地关注，自适应卡片可调大小并进入独立详情，支持筛选名称/UID、仅看待提交、刷新/重试、历史详情及跳转投稿发现；切换服务器公开库后搜索并选择关注 |
| 视频库 | 同上，支持BV/标准链接直接关注，播放指标与历史版本；本期不提供媒体下载 |
| 投稿发现 | 原有Cookie扫描、进度、停止/继续、分页、复制导出；结果逐项关注时自动申请服务器跟踪 |
| 设置 | 服务器地址与连接测试、数据工作目录迁移、Cookie验证与清除 |

主路由为 `#overview / #creators / #videos / #discovery / #settings`，详情为 `#creators/{UID}`、`#videos/{BV}`，公开来源保留 `?scope=public`。除启动密钥交换外，页面刷新和前进/后退保留当前路由；手机通过可收起侧栏导航。页面切换不停止Agent任务。

搜索分为本地关注筛选与服务器公开库查询，后者只读服务器已收录数据，不代表B站全站关键词搜索。UID/BV/标准链接按精确标识查询，名称按服务器既有搜索处理；未收录的目标可直接按标识关注，由服务器验证。公开跟踪状态不等于“我已关注”，搜索结果的个人标记由Agent本地计算。

## 自动提交与重试

8181正常业务HTTP200，Agent检查JSON的ok/code并保留结构化失败；无资料缓存不再显示不可达，见 [协议](public-response.md)。

`POST /api/follows` 统一调用 `library.Service.Follow`：先保存本地关注，再请求8181 `POST /targets`；没有“只本地关注不提交”的界面开关。手动输入、公开搜索结果和扫描结果使用同一路径。

远程未配置、断连、拒绝或忙时仍保留本地关注，响应明确返回 `pending_submission` 与 `last_error`，不能将本地保存成功当作服务器跟踪成功。已确认返回 `remote.tracking=true` 并清除待提交状态；取消只删除本地项，不发送全局停采命令。

`library.RunPending` 绑定Agent生命周期，不依赖网页保持打开：启动后及连接更改时检查队列，正常每30秒一轮，每轮最多3个到期项；失败按1、2、4、8、15分钟退避，连续6次失败转为人工处理；重启保留下一次尝试时间。已确认目标每15分钟分批复核，启动后旧确认也重新查询。一个目标在本机同时只允许一个核对/提交；服务器继续按公共目标去重。手动重试可提前发起，批量按钮每次最多处理5项，其余由后台继续。

自动重试只协调已关注目标的服务器申请，不在本地周期采集粉丝/播放指标。更换服务器清除旧公共资料和总览缓存，保留私人关注并重新排队。浏览当前页、打开详情、后台到期和手动重试均通过精确状态核对；无任务才补申请，暂停不恢复。查询失败保留未知，连续失败有界处理。实现见 [对账计划](../PLANs/2026-11-16-agent-follow-reconciliation.md) 和 [公共JSON协议](public-response.md)，摘要读取与跟踪确认仍为不同数据。

## 公开信息接口

| 服务器路径 | 复用方式 |
|---|---|
| `GET /creators`、`GET /videos` | 沿用既有 `q/id/page` 查询，Agent本地补个人关注标记，不写入跟踪；总览依次读取两库首页，以各自 `total` 作数量，返回完整首页与 `limit`，后续翻页复用同一库接口，不改变服务器默认排序 |
| 详情、状态及提交 | 继续复用 [原接入协议](client-library.md#8181-公共协议)；详情允许查看尚未本地关注的公开对象 |

本地新增 `GET /api/overview`（本地数量与最近总览）、`POST /api/overview/refresh`（聚合既有库接口）、`GET /api/remote/catalog?kind=creator&q=...&page=1`、`GET /api/settings` 和 `PUT /api/settings/directory`。当前共27个API，清单见 [Agent说明](../client/agent/README.md#api-接口)。详情可选已有时间范围，协议补充见 [详情说明](client-catalog.md#本地接口与边界)。所有本地接口继续检查会话、Host/Origin和写操作CSRF，不转发Cookie。

Agent不请求服务器根路径JSON或解析管理员HTML，也不推算缺失的全局统计。两库读取共用20秒预算，任一失败则保留原缓存；两次读取不是服务器同一时刻的全局快照。总览持久化标记 `catalog_based`，旧版根接口及五条预览缓存不再展示，个人关注和其他缓存保留。总览与查询结果标明来源和时间，网络失败保留上次兼容总览与个人关注；初次未读取时显示未知，不用0冒充服务器统计。所有服务器文本以文本节点显示，不执行标题中的HTML。

## 工作目录

设置填写本机绝对路径，确认后在同一个存储锁内复制 `library.json`、`scan.json`、Windows `cookie.protected`，目标目录必须为空，原件不删除。复制成功后才切换活动目录，后续关注和扫描写入新位置；不能选当前目录的子目录或覆盖已有数据。

初始 `-data` 目录保留一个 `location.json` 位置文件，使沿用原启动命令时仍能找到新工作目录；其余旧状态文件作为原始副本保留。该位置文件是启动指向，不是可迁移备份；删除它或直接用另一个旧目录启动会改变加载位置。程序验证指向格式、目录可用性与循环/层级，无法读取时明确报错，不悄悄创建空档案。

新目录内包括当前全部持久状态；初始位置文件仍在启动目录。非Windows Cookie继续只保留在内存，目录迁移不改变其保护策略。当前不迁移用户任意附加文件，也不提供完整档案备份/恢复或云盘同步；这些仍见 [数据管理计划](client-data-management.md)。

## 实现与验证

- 浏览器：[navigation.js](../client/frontend/navigation.js) 负责侧栏/路由，[library.js](../client/frontend/library.js) 负责工作台数据与操作，[app.js](../client/frontend/app.js) 保留会话和扫描。
- Agent：[library.Service](../client/agent/internal/library/service.go)、[pending.go](../client/agent/internal/library/pending.go)、[remote.Client](../client/agent/internal/remote/client.go)、[目录迁移](../client/agent/internal/localstate/directory.go)；HTTP入口见 [library.go](../client/agent/internal/httpapi/library.go)。
- 测试：[业务/队列测试](../client/agent/internal/library/workspace_test.go)、[目录测试](../client/agent/internal/localstate/directory_test.go)、[设置权限测试](../client/agent/internal/httpapi/settings_test.go)、[既有库聚合测试](../client/agent/internal/remote/client_test.go)、[前端状态/路由测试](../client/frontend/view.test.mjs)。
以下条目记录早期工作台版本的验证，当时结论不替代本版检查；当前能力与验证见 [库与缓存说明](client-catalog.md)。

- 当时后端3个文件与 `7c8be78` 一致；既有库聚合测试覆盖空库、总数/完整分页与不截断、禁用根接口和任一库失败，业务测试覆盖旧缓存失效与重启保留。两个Go模块测试、Agent vet/构建、前端检查及15项Node测试通过。Edge真实Agent联调覆盖五个页面、公开搜索分页且不写入、未关注对象详情、自动关注、迁移后从原启动目录重启、断网缓存/待提交、手机导航及无横向溢出；原135条扫描、分页、复制导出和停止/继续回归通过。服务器/平台使用本地模拟数据，未使用真实Cookie或宣称已更新远端部署。

基础工作台复用既有服务器接口；本版新增公开排序和 UP 投稿统计需更新 backend，摘要缓存修复只需更新本机 Agent。全局跟踪/近期版本总览继续暂缓。当前安全部署方式不变，继续按 [接入边界](client-library.md#本地-api-与边界) 使用可信服务或SSH隧道。

## issue-agent-ui-unresponsive

本节保留2026-11-16上午的原始诊断记录。当前代码已固定所有 JS/MJS 模块的 JavaScript MIME，并增加错误机器映射下 GET/HEAD 回归及 Edge 初始化验证；原文中的“待修复”“未修改代码”等表示当时状态，当前实现与验收边界见 [UI 计划](../PLANs/2026-11-16-client-ui-content.md) 和 [模块测试](../client/agent/internal/httpapi/catalog_test.go)。

### 问题记录与范围

- 记录时间：2026-11-16 10:14（Asia/Shanghai）；状态：待修复。
- 用户反馈：“本地 Agent 启动了，浏览器卡死，没反应”，并要求先记录问题和分析，不改代码。
- 截图地址为 `http://127.0.0.1:8183/#settings`，正文仍显示信息总览，公开统计为占位符，浏览器标签仍为初始标题“Bili History Tracker · 个人工作台”。截图中的公开列表文案也与初始 HTML 一致。
- 截图证明路由片段与页面内容不一致；不能单凭截图确认主线程阻塞、浏览器进程死锁或 Agent 业务服务完全失效。也不能把初始显示的 0 当作真实本地关注数量。
- 本次仅阅读源码、对运行中 Agent 发起静态资源 HEAD/GET 请求并更新文档；未修改业务/测试代码、注册表、配置和数据，未建立 API 会话、重启 Agent 或运行修复后的浏览器验收。

### 当前响应证据

2026-11-16 10:15（Asia/Shanghai）直接请求 `127.0.0.1:8183`，绕过代理，得到：

| 资源 | 方法 | 状态 | Content-Type |
|---|---|---|---|
| `/` | HEAD | 200 | `text/html; charset=utf-8` |
| `/app.js` | HEAD | 200 | `application/javascript` |
| `/library.js` | HEAD | 200 | `application/javascript` |
| `/navigation.js` | HEAD | 200 | `application/javascript` |
| `/view.mjs` | HEAD、GET | 200 | `text/plain; charset=utf-8` |

以上响应均包含 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff` 和 CSP；`/view.mjs` 的 GET 响应长度为 1752 字节。因此当前服务可响应静态资源，但 HTTP 200 不代表 JavaScript 模块可被浏览器执行。已确认错误 MIME；尚未取得受影响浏览器控制台的实际拒绝信息，也未核对运行二进制与当前源码的构建对应关系。

### 原因分析

首要原因线索是模块以纯文本类型提供。浏览器模块加载要求 JavaScript MIME，`text/plain` 不满足要求；入口及其依赖的模块加载失败，会使入口初始化无法执行。此机制足以解释现有现象，但是否还有其他独立问题，需要在纠正资源响应后复测。

当前源码执行链路为：

```text
index.html 加载 app.js（type="module"）
  -> 加载 view.mjs、library.js、navigation.js
  -> initLibrary 绑定工作台控件
  -> initNavigation 绑定侧栏和 hashchange
  -> navigation.update() 应用当前路由
  -> connect(token) 建立/恢复会话并读取业务数据
```

代码依据：

- [index.html](../client/frontend/index.html) 默认展示信息总览，其他页面带 `hidden`；侧栏使用普通 `href="#settings"` 等锚点。即使脚本未执行，链接仍可能改变地址片段，而不能完成应用页面切换。
- [app.js](../client/frontend/app.js) 顶层导入 `view.mjs`、`library.js` 和 `navigation.js`；后两者也依赖 `view.mjs`。导航绑定和首次 `navigation.update()` 均发生在 `connect(token)` 调用之前。缺少连接密钥或业务请求失败，通常应显示连接提示，不足以单独解释导航完全未初始化。
- [navigation.js](../client/frontend/navigation.js) 的 `initNavigation` / `update` 会切换 `[data-page]`、设置当前导航并修改标签标题。截图标题与默认内容未改变，符合入口初始化没有完成的表现。
- [assets.go](../client/frontend/assets.go) 显式内嵌 `view.mjs`，当前 HTTP 响应也不是 404；[server.go](../client/agent/internal/httpapi/server.go) 的 `New` 使用 `http.FileServerFS(frontend.Files)`，未为模块扩展名显式指定 MIME，`ServeHTTP` 设置 `nosniff` 与 CSP。

MIME 异常的具体来源仍待核实。本机可读的 Go 标准库 `mime/type_windows.go` 显示 Windows MIME 初始化会读取注册表扩展名的 `Content Type`，且对 `.js` 的 `text/plain` 有专门规避，但该分支不包含 `.mjs`。这支持将系统扩展名映射作为检查方向，不能据此断言当前注册表已有错误配置，或运行 Agent 一定由该工具链构建。

其他候选原因包括资源版本不一致、模块解析/导入错误，以及 `initLibrary` 在导航初始化之前发生 DOM 访问异常。若后续浏览器仍不响应，应以控制台首条错误继续定位。当前不以重配 Cookie、远端服务器或迁移 Qt 作为此故障的已证实解决方案。

### 测试缺口与后续验证

[server_test.go](../client/agent/internal/httpapi/server_test.go) 的 `TestStaticAssetsAndInvalidInputs` 对静态资源仅检查状态码和 CSP 是否存在，未断言 JavaScript MIME，清单也未包含 `library.js`、`navigation.js`。[package.json](../client/package.json) 的 lint 仅检查语法，test 运行 [view.test.mjs](../client/frontend/view.test.mjs) 的纯函数测试；其中路由用例检查 `viewForHash`，并未在真实浏览器中加载模块和点击侧栏。因此这些检查即使通过，也不能排除此类部署响应问题。

后续应先采集受影响标签的控制台首条错误、模块 Network 响应及实际构建版本，再考虑在静态资源层固定模块 MIME；不能通过删除 `nosniff` 或放宽 CSP 掩盖问题。补充静态资源 MIME 回归及 Windows 浏览器启动、直接访问设置、五页导航、会话缺失和远端不可达验收，并评估启动失败时的可见提示。详细待办与完成条件见 [工作台无响应计划](../PLANs/2026-11-16-agent-ui-unresponsive.md)。本次未运行测试套件，未宣称问题已修复。
