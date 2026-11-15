# 客户端工作台与关注操作

状态：已实现。当前仍为 Go Agent + 浏览器，Qt 重构继续保留未来计划；本次提供侧栏工作台、公开搜索、关注自动提交与工作目录设置。

## 页面与来源

| 侧栏 | 内容与操作 |
|---|---|
| 信息总览 | 服务器公开库的 UP/视频数量、公共目标、近7日版本变化、UP精选、最近视频及元数据更新；单独展示本地关注与待提交数量 |
| UP主库 | 默认显示本地关注，支持筛选名称/UID、仅看待提交、刷新/重试、历史详情及跳转投稿发现；切换服务器公开库后搜索并选择关注 |
| 视频库 | 同上，支持BV/标准链接直接关注，播放指标与历史版本；本期不提供媒体下载 |
| 投稿发现 | 原有Cookie扫描、进度、停止/继续、分页、复制导出；结果逐项关注时自动申请服务器跟踪 |
| 设置 | 服务器地址与连接测试、数据工作目录迁移、Cookie验证与清除 |

路由为 `#overview / #creators / #videos / #discovery / #settings`。除启动密钥交换外，页面刷新和前进/后退保留当前路由；手机通过可收起侧栏导航。页面切换不停止Agent任务。

搜索分为本地关注筛选与服务器公开库查询，后者只读服务器已收录数据，不代表B站全站关键词搜索。UID/BV/标准链接按精确标识查询，名称按服务器既有搜索处理；未收录的目标可直接按标识关注，由服务器验证。公开跟踪状态不等于“我已关注”，搜索结果的个人标记由Agent本地计算。

## 自动提交与重试

`POST /api/follows` 统一调用 `library.Service.Follow`：先保存本地关注，再请求8182 `POST /targets`；没有“只本地关注不提交”的界面开关。手动输入、公开搜索结果和扫描结果使用同一路径。

远程未配置、断连、拒绝或忙时仍保留本地关注，响应明确返回 `pending_submission` 与 `last_error`，不能将本地保存成功当作服务器跟踪成功。已确认返回 `remote.tracking=true` 并清除待提交状态；取消只删除本地项，不发送全局停采命令。

`library.RunPending` 绑定Agent生命周期，不依赖网页保持打开：启动后及连接更改时检查队列，正常每30秒一轮，每轮最多3个到期项；失败按1、2、4、8、15分钟退避，重启保留下一次尝试时间。一个目标在本机同时只允许一个提交；服务器继续按公共目标去重。手动重试可提前发起，批量按钮每次最多处理5项，其余由后台继续。

自动重试只协调已关注目标的服务器申请，不在本地周期采集粉丝/播放指标。更换服务器清除旧公共资料和总览缓存，保留私人关注并重新排队。若服务器对目标作永久拒绝，当前客户端仍按退避重试，后续可增加明确的终止/暂停策略。

## 公开信息接口

| 服务器路径 | 复用方式 |
|---|---|
| `GET /`，`Accept: application/json` | 新增只读公开总览，仅统计、公开内容和近期版本，不返回管理员日志或错误列表；默认HTML不变 |
| `GET /creators`、`GET /videos` | 沿用既有 `q/id/page` 查询，Agent本地补个人关注标记，不写入跟踪 |
| 详情、状态及提交 | 继续复用 [原接入协议](client-library.md#8182-复用协议)；详情允许查看尚未本地关注的公开对象 |

本地新增 `GET /api/overview`（本地数量与最近总览）、`POST /api/overview/refresh`（读取服务器）、`GET /api/remote/catalog?kind=creator&q=...&page=1`、`GET /api/settings` 和 `PUT /api/settings/directory`。当前共24个API，清单见 [Agent说明](../client/agent/README.md#api-接口)。所有本地接口继续检查会话、Host/Origin和写操作CSRF，不转发Cookie。

总览缓存与查询结果标明来源和时间，网络失败保留上次公开总览与个人关注；初次未读取时显示未知，不用0冒充服务器统计。所有服务器文本以文本节点显示，不执行标题中的HTML。

## 工作目录

设置填写本机绝对路径，确认后在同一个存储锁内复制 `library.json`、`scan.json`、Windows `cookie.protected`，目标目录必须为空，原件不删除。复制成功后才切换活动目录，后续关注和扫描写入新位置；不能选当前目录的子目录或覆盖已有数据。

初始 `-data` 目录保留一个 `location.json` 位置文件，使沿用原启动命令时仍能找到新工作目录；其余旧状态文件作为原始副本保留。该位置文件是启动指向，不是可迁移备份；删除它或直接用另一个旧目录启动会改变加载位置。程序验证指向格式、目录可用性与循环/层级，无法读取时明确报错，不悄悄创建空档案。

新目录内包括当前全部持久状态；初始位置文件仍在启动目录。非Windows Cookie继续只保留在内存，目录迁移不改变其保护策略。当前不迁移用户任意附加文件，也不提供完整档案备份/恢复或云盘同步；这些仍见 [数据管理计划](client-data-management.md)。

## 实现与验证

- 浏览器：[navigation.js](../client/frontend/navigation.js) 负责侧栏/路由，[library.js](../client/frontend/library.js) 负责工作台数据与操作，[app.js](../client/frontend/app.js) 保留会话和扫描。
- Agent：[library.Service](../client/agent/internal/library/service.go)、[pending.go](../client/agent/internal/library/pending.go)、[remote.Client](../client/agent/internal/remote/client.go)、[目录迁移](../client/agent/internal/localstate/directory.go)；HTTP入口见 [library.go](../client/agent/internal/httpapi/library.go)。
- 测试：[业务/队列测试](../client/agent/internal/library/workspace_test.go)、[目录测试](../client/agent/internal/localstate/directory_test.go)、[设置权限测试](../client/agent/internal/httpapi/settings_test.go)、[服务器只读测试](../backend/internal/admin/client_test.go)、[前端状态/路由测试](../client/frontend/view.test.mjs)。
- 两个Go模块测试/vet、前端检查及6项Node测试通过。Edge真实Agent联调覆盖五个页面、公开搜索分页且不写入、未关注对象详情、自动关注、迁移后从原启动目录重启、断网缓存/待提交、手机导航及无横向溢出；原135条扫描、分页、复制导出和停止/继续回归通过。服务器/平台使用本地模拟数据，未使用真实Cookie或宣称已更新远端部署。

部署时需更新 backend 以提供新增JSON总览，再更新本地Agent。既有公开库接口仍可用于搜索；当前安全部署方式不变，继续按 [接入边界](client-library.md#本地-api-与边界) 使用可信服务或SSH隧道。
