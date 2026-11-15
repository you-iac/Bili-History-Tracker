# 客户端卡片库、详情与总览来源

当前实现只修改本地客户端，复用已部署服务器的8182 JSON接口，不要求修改backend。

## 库与详情

UP主库、视频库以卡片显示名称、UID/BV、最近观测指标、内容版本、近7日日采样净变化及本地关注状态。卡片上的公共采集状态与私人关注分别展示，点击卡片或标题进入独立详情；管理菜单保留刷新、重试、投稿发现及取消本地关注。公开库仍支持搜索、分页和关注，读取不会自动申请跟踪。

“卡片大小”可选紧凑、标准、宽松，网格随可用宽度自动调整列数，手机收为单列。仅这个显示偏好存于当前浏览器 `bht.card-size`，不同浏览器可独立选择；Cookie和会话密钥不写入浏览器存储。数据本身继续保存在Agent工作目录。

详情地址为 `#creators/{UID}` 或 `#videos/{BV}`；来自公开库时保留 `?scope=public`。支持刷新、原生链接打开新标签、前进/后退及返回来源库；库在同一页面会话中保留查询和页码。详情展示最近观测、个人关注、真实指标趋势、内容版本分页与前后版本差异，可在此关注/重试、取消和进入原投稿扫描。

趋势可选7/30/90日或全部时间，采样日期沿用服务器UTC日口径，其他观测时间按北京时间展示。只绘制真实点，同一UTC日取最新值，缺失日期断线，负增长保留，少于两个采样不计算净变化；精确值可展开表格。服务器采样上限通过 `truncated` 提示，不把返回的点当作完整永久历史。

选择版本后读取所选及上一版本快照，对比名称、签名、标题、简介等稳定内容字段；首次记录作为基线，计数变化不列为内容修改。上一版本读失败时不伪造差异，仍可查看所选原始快照。当前字段对比是便于阅读的摘要，完整JSON可展开。

## 总览为什么曾经只有五条

之前的“五条”来自本地 [remote.Client.Overview](../client/agent/internal/remote/client.go) 的 `[:min(5,...)]` 截断，不是服务器数据上限。现在已移除截断，总览显示服务器返回的完整第一页，两个列表各自可翻页，并提供“查看全部”进入完整公开库。服务器当前每页18条，具体页长以响应 `limit` 为准，不一次性拉取所有页。

数据由以下模块协作提供，返回沿原路径回传：

```text
+-------------------------------+
| frontend: overview / catalogs |
+-------------------------------+
                | local HTTP
                v
+-------------------------------+
| httpapi: authenticated routes |
+-------------------------------+
                | call
                v
+-------------------------------+
| library: local state / cache  |
+-------------------------------+
                | read via remote.Client
                v
+-------------------------------+
| 8182: catalogs / detail JSON  |
+-------------------------------+
                | read
                v
+-------------------------------+
| storage: recorded public data |
+-------------------------------+
```

浏览器 [library.js](../client/frontend/library.js) 读取本地 `GET /api/overview`；手动/进入总览刷新走 `POST /api/overview/refresh`，由 [httpapi/library.go](../client/agent/internal/httpapi/library.go) 调用 [library.Service.RefreshOverview](../client/agent/internal/library/service.go)，再由 [remote.Client.Overview](../client/agent/internal/remote/client.go) 读取服务器既有 `/creators` 和 `/videos`。数量直接使用 `total`，第一页内容和 `limit` 缓存在本地。后续翻页走既有 `GET /api/remote/catalog?kind=...&page=...`，不增加服务器接口。

服务器的既有 [admin.clientLibrary](../backend/internal/admin/client.go) 调用 [storage.Catalog](../backend/internal/storage/dashboard.go) 读取公共记录；管理员HTML页面也调用服务器查询层，所以能展示服务器内部已有的更多字段。客户端只能使用现有JSON协议暴露的数据，二者不是完全相同的页面模型。本轮没有改动这些后端文件。

两个库的总数分别读取，不是服务器同一事务快照。网络失败保留兼容缓存与时间，不用0冒充未知统计；旧的五条预览缓存会失效重读，私人关注不受影响。总览翻页失败保留原页，不悄悄显示空库。

## 接口与边界

本地仍为24个API。`GET /api/library/{kind}/{id}?days=7|30|90|all&page=1&seq=...` 将时间范围传给服务器已有详情接口，并附带 `server_url`、`local_follow`、`cached/read_error`；本地关注摘要不重复携带整份资料缓存。默认30日第一页在服务器断连时可展示本地已存资料，其他范围、版本页和快照不使用不匹配的缓存冒充。范围切换失败会恢复上次成功的选项与图表，并提示失败。

关注始终先本地保存再提交服务器，失败保留待提交状态；取消只影响本地。详情刷新只读已有记录，不触发管理员强制采集。会话、Host/Origin、写入CSRF、固定路径、重定向拒绝及不转发Cookie的约束保持不变。

现有JSON没有提供管理员卡片里的头像/封面地址、UP已记录视频总数等完整字段，因此当前使用文字/图形占位和已提供的内容版本数，不为每张卡片额外请求快照，也不改后端补字段。全局跟踪统计与近期全库版本动态仍暂缓。投稿扫描得到的列表不等同于服务器完整投稿库。

## 模块与验证

- [cards.js](../client/frontend/cards.js)、[catalog.css](../client/frontend/catalog.css)：自适应卡片和显示密度；[navigation.js](../client/frontend/navigation.js)、[view.mjs](../client/frontend/view.mjs)：白名单详情路由与格式化。
- [detail.js](../client/frontend/detail.js)、[trend.mjs](../client/frontend/trend.mjs)：详情、内容对比、UTC真实趋势和过期请求保护。
- [HTTP详情测试](../client/agent/internal/httpapi/details_test.go)、[业务/缓存测试](../client/agent/internal/library/workspace_test.go)、[远程测试](../client/agent/internal/remote/client_test.go)：范围白名单、只读、个人状态、匹配缓存及不截断总览。
- [前端状态/路由测试](../client/frontend/view.test.mjs)、[趋势测试](../client/frontend/trend.test.mjs)：深链接、无效标识、内容字段、UTC去重、日期缺口、未知及负值。

浏览器联调使用真实Agent会话/本地存储与模拟服务器，覆盖390/768/1440/1920宽度、三档大小及偏好、总览18条与第二页、详情刷新/新标签、版本分页与差异、关注和离线回退；不使用真实Cookie，不表示已部署远端。原扫描流程继续独立验证。当前工作台其他行为见 [工作台说明](client-workspace.md)。
