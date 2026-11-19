# 客户端卡片库、UP 视频与详情

后续调整见 [作者详情的数据来源与服务器回退设计](client-detail-data-sources.md) 与 [实施任务](../PLANs/2026-11-19-client-detail-data-sources.md)：Agent 优先获取当前资料，服务器提供历史与跟踪，平台失败再读服务器最后保存资料，版本按需加载。本分支仅交付设计文档，服务器接口、Agent 与页面改造均待实施；下文仍描述现有行为。

本轮实现见 [UI 优化计划](../PLANs/2026-11-16-client-ui-content.md)。原计划已单独提交，本版收录 UI 与摘要缓存修复；[跟踪状态对账](../PLANs/archive/2026-11-16-agent-follow-reconciliation.md) 已由 [公共JSON计划](../PLANs/archive/2026-11-17-public-json-response.md) 扩展实施，详见 [协议](public-response.md)。浏览器资源内嵌在 Agent，修改后需要重新构建并启动 Agent；公开库的新增排序和 UP 投稿统计需要同步更新 backend，然后刷新资料。旧资料缺少字段时显示未知。

## 页面与卡片密度

UP 主库和视频库只保留一行标题与添加入口，不再重复展示全局页头和介绍。“本地工作空间”位于侧栏底部。库在同一会话中保留范围、查询、排序和页码。

三个目录共用 [cards.js](../client/frontend/cards.js) 和 [catalog.css](../client/frontend/catalog.css)：

| 密度 | 展示内容 |
|---|---|
| 紧凑 | 头像/小封面、名称、UID/BV、粉丝/播放基本指标、详情及管理/关注操作 |
| 标准 | 头像/封面、身份、粉丝/播放量、近7日真实观测增量、操作 |
| 宽松 | 标准信息，加发布时间；UP 增加近7日已记录新投稿数和已记录视频播放合计 |

卡片不再显示采集状态、公共跟踪状态和内容版本数；本地提交/读取错误仍有可见提示。管理菜单支持刷新、待提交重试和取消本地关注。公开视频中已关注的目标也能直接管理。只被扫描发现、尚未记录的视频可在 B站打开，关注后才申请持续跟踪。

密度偏好存于当前浏览器 `bht.card-size`，两个库与 UP 视频区同步；Cookie 和会话密钥不写入浏览器存储。手机布局收为单列。

## 搜索与排序口径

库搜索和添加关注接受名称、UID/BV、标准 B站链接，包括查询参数。UID/BV 与其链接解析后使用相同标识精确查询；普通名称采用文本搜索。UP 示例 `72541187`、`https://space.bilibili.com/72541187` 和带 `spm_id_from` 的空间链接等价。视频支持 `www.bilibili.com/video/BV...`、主域名和移动域名的标准视频路径。链接只做白名单解析，不访问用户输入网址；短链解析和 B站全站文本搜索不在此范围内。

| 排序 | 口径 |
|---|---|
| 粉丝数 / 播放量 | UP 使用最新粉丝数；视频使用最新播放量 |
| UP 的播放量 | 服务器已记录所属视频的最新播放量合计，不能当作 UP 全站累计播放 |
| 最近热门 | 近7个 UTC 日内首末真实日采样的粉丝/播放差值，至少两个采样；未知项保留在末尾 |
| 最新更新 | 视频发布时间；UP 使用已记录视频的最新发布时间；未记录投稿时为未知 |
| 名称 | 名称排序，同值按 ID 保持稳定顺序 |
| 最近关注 | 本地关注时间，只在本地库可选 |

筛选和排序均在分页之前应用到完整结果集。公开库由 [storage.Catalog](../backend/internal/storage/dashboard.go) 排序，新增 `hot` 保留缺少采样的条目，既有管理员 `growth` 排行仍可只看已知增量。本地库和已获取投稿由 [library/catalog.go](../client/agent/internal/library/catalog.go) 在 Agent 内排序，不只重排当前页。服务器发布时间为毫秒，扫描为秒，前端显示和本地排序兼容两类历史数据。

UP 宽松卡片的“近7日新投稿”仅统计服务器已记录、发布时间位于近7个 UTC 日范围内的视频，零表示该已记录范围内没有新投稿。旧服务器未提供该字段时显示未知，不补造零值。JSON 在既有目录/详情中增加 `video_count`、`video_views` 和 `recent_videos`，无需数据库迁移。

## 本地卡片资料与详情缓存

本地关注是否已提交，与服务器是否已有资料分开处理。切换服务器会清空旧来源的摘要和详情；之前卡片只读本地详情缓存，且回填绑定关注提交成功，导致“名称只有 UID、粉丝和变化为未知，但打开详情可以看到数据”。现在由 [summaries.go](../client/agent/internal/library/summaries.go) 独立读取资料；浏览同步会先核对当前页已关注目标，明确无任务才申请，实际采集由服务器管理。

本地库先显示缓存，再按当前页ID精确同步，最多24项、20秒；不再遍历公共全库。同一页目标集合60秒内复用，翻页/筛选后的新集合独立核对；手动“刷新库资料”可提前同步摘要。UP详情的视频区同样只处理当前页。请求按服务器代际、类型和ID集合合并；中途失败保留部分结果和旧缓存。

关注条目的 summary/summary_at 保存名称、指标、归属等轻量资料，卡片与排序优先使用摘要，兼容旧 data.item。读取详情也更新摘要；仅默认30日、第一页、未指定快照的详情更新完整 data 缓存。7/90日、历史分页和版本快照只更新当前资料摘要，不能覆盖默认离线趋势。缓存写入失败仍展示成功读取的详情并提示保存失败。

所有缓存写回在锁内核对当前服务器代际和原关注身份，切服后返回的旧请求被拒绝，取消关注不被读请求重新创建。摘要写入本身不确认跟踪；浏览触发的独立Reconcile可更新pending_submission、核对时间和重试状态。关注身份使用随机follow_identity与创建时间，不能仅凭时间戳判断取关后是否同一次关注。

无资料详情通过read_code/read_error/target表达真实业务状态，有缓存时继续显示旧资料，无缓存时显示目标和关注状态，不伪造指标。HTTP200业务失败仍按ok/code识别，不能当作资料读取成功。

## UP 详情内的视频

详情路由为 `#creators/{UID}` 或 `#videos/{BV}`，公开库来源保留 `?scope=public`。UP 头像与资料位于左侧，粉丝数和真实趋势位于右侧；小屏纵向排列。视频区接在首屏之后，版本时间线与内容差异保留在页面末尾。

[creator-videos.js](../client/frontend/creator-videos.js) 编排两个列表：

- 已关注视频：本地私人关注中属于该 UP 的视频，包括该 UP 扫描上下文中的合作投稿。已关注但从未获取任何归属资料的旧条目暂不猜测归属，刷新或重新获取投稿后可关联。
- 未关注投稿：当前 UP 扫描中已经获取且尚未本地关注的视频；支持同样的搜索、排序、卡片密度、分页和逐条关注。

点击“获取全部视频”或“获取全部未关注视频”留在详情，显式启动原扫描任务并切换到未关注列表；支持进度、停止和继续。无 Cookie 时展示设置入口。扫描仍是全局单任务，另一 UP 正在扫描时不会在当前详情停止别人的任务；浏览页面不会自动发起平台扫描。

“全部”仍指登录态接口可枚举的可见投稿。部分、失败、取消或中断状态不称为完整；扫描标题、封面、播放量和发布时间来自平台分页。新发现投稿没有历史日采样，“最近热门”的历史增量保持未知。平台未提供播放量或封面时保留未知或占位。

关注先在本地保存，再申请服务器公共跟踪；失败保留待提交。已关注条目从未关注列表消失，已关注区即时更新；取消只影响本地。关注时将扫描来源 UP 和基本资料保存为私人 `discovery_creator_id/discovery`，扫描被其他 UP 替换或远程读取失败后仍保留关联；这些资料不会冒充服务器成功观测，也不会改写视频主作者。切页、返回和切换 UP 时使用请求代际避免旧结果覆盖当前页面。

数据协作如下，响应沿原路径返回：

```text
+----------------------------------------+
| frontend: library / detail / UP videos  |
+----------------------------------------+
                   | authenticated local HTTP
                   v
+----------------------------------------+
| httpapi: query / follow / explicit scan |
+----------------------------------------+
          | read and sort       | start/read task
          v                     v
+-----------------------+  +-----------------------+
| library: follows      |  | scanner: visible posts|
| local cache / context |  | local Cookie / resume |
+-----------------------+  +-----------------------+
          | fixed public JSON       | platform HTTPS
          v                         v
+-----------------------+  +-----------------------+
| 8181 / storage        |  | Bilibili              |
| recorded public data  |  | logged-in visibility  |
+-----------------------+  +-----------------------+
```

## 趋势、版本与总览

趋势可选7/30/90日或全部时间，采样日期沿用服务器 UTC 日口径，其他时间按北京时间显示。同日取最新值，只画真实点，缺失日断线，负增长保留；少于两个采样不算净变化。精确值可展开表格，`truncated` 提示服务器采样上限。

选择版本后比较该版与上一版的稳定内容字段；首次记录作为基线，计数变化不列为内容修改。上一版读取失败时保留所选原始快照，不伪造差异。历史分页和路由刷新、新标签、返回来源保持原有能力。

总览继续用 `/creators`、`/videos` 的 `total` 和完整第一页，不再截为五条；两个列表独立翻页，页长以响应为准。两个总数不是同一事务快照。总览失败保留最近缓存和时间；全局跟踪统计与近期全库版本动态仍暂缓。

## 本地接口与边界

本地共27个 API，完整清单见 [Agent 说明](../client/agent/README.md#api-接口)。

| 接口 | 本轮变化 |
|---|---|
| `POST /api/follows/refresh?kind=creator或video&id=...` | 同步最多24个当前页ID，显式ID先核对；缺省最多24项只同步摘要，受CSRF保护 |
| `GET /api/follows?kind=video&q=&sort=published&owner=123&page=1` | 链接归一化、本地全量排序和归属过滤，仍每页20条 |
| `GET /api/remote/catalog?kind=video&q=&sort=hot&owner=123&page=1` | 向8181公共目录透传排序和 UP 归属，补本地关注标记 |
| `GET /api/creator-videos/{UID}?scope=followed或discovered&q=&sort=published&page=1` | 只读组合 `catalog/job/configured/busy_elsewhere`，不启动扫描 |
| `POST /api/scan`、取消、继续 | 复用原有扫描路径；详情不跳转 |
| `POST /api/follows` | 若视频来自当前扫描，同时保留私人投稿来源上下文 |

详情接口仍为 `GET /api/library/{kind}/{id}?days=7|30|90|all&page=1&seq=...`，附带 `server_url/local_follow/cached/read_error`。默认30日第一页断连可回退匹配的已存详情，其他范围和历史快照不冒用缓存；切换范围失败恢复上次成功范围。

会话、Host/Origin、写入 CSRF、固定远程路径、拒绝重定向和 Cookie 不转发约束保留。头像和服务器封面继续通过既有历史快照按需解析 URL，扫描有直接封面时沿用浏览器图片域名白名单；失败保留占位。Windows 静态资源层明确返回所有 `.js/.mjs` 的 JavaScript MIME，避免机器注册表映射成纯文本后被 `nosniff` 阻断整个界面。

## 验证与模块导航

- [摘要业务测试](../client/agent/internal/library/summaries_test.go)、[摘要 HTTP 测试](../client/agent/internal/httpapi/summaries_test.go)：空缓存/待提交仍可读取、目录分页、部分失败、切服及取关隔离、默认详情范围回填、落盘失败和 CSRF。
- [本地目录测试](../client/agent/internal/library/catalog_test.go)：全量排序再分页、未知末尾、链接等价、秒/毫秒、UP 归属、关注切换与来源上下文。
- [HTTP 目录测试](../client/agent/internal/httpapi/catalog_test.go)：只读不启动扫描、不同 UP 隔离、参数白名单、链接搜索、错误机器 MIME 映射下全部模块的 GET/HEAD。
- [远程透传测试](../client/agent/internal/remote/catalog_test.go)、[存储目录测试](../backend/internal/storage/catalog_test.go)：排序和 owner 传递、UP 聚合统计、跨页顺序、未知增量保留。
- [详情测试](../client/agent/internal/httpapi/details_test.go)、[前端状态测试](../client/frontend/view.test.mjs)、[趋势测试](../client/frontend/trend.test.mjs)：原有路由、缓存、版本和日采样约束。

本轮执行客户端 pnpm lint/test、两模块 Go test/vet/build。浏览器使用隔离 Agent 数据、模拟8182和图片响应，覆盖390/768/1440/1920宽度、三档实际内容差异、左右布局、归属列表跨页、链接搜索、排序、关注/取关和部分扫描结果；详情内启动按钮使用模拟扫描响应，真实任务流程由 Go 平台桩测试覆盖。不使用真实 Cookie 或数据库，不代表真实 B站网络扫描或远程部署已验收。用户实际体验仍待确认。

本轮体验回归增加“全部本地资料缓存为空且公共跟踪提交持续失败”的浏览器场景：卡片自动出现名称、粉丝数和近7日变化，待提交状态保留。此修复只需更新本机 Agent，虚拟机 backend 无需替换。
