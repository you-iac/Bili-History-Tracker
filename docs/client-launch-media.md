# Agent自动打开网页与头像封面

本次只更新本地客户端，服务器既有8182接口保持不变。

## 启动与认证

在 `client/` 执行 `go run ./agent/cmd/agent`，或运行重新构建的Agent程序。默认监听 `127.0.0.1:8183`，服务开始监听后请求系统默认浏览器打开完整本地链接；浏览器自动交换本地会话并从地址栏移除密钥，不需要用户复制或输入token。

Windows使用默认URL处理程序，macOS使用 `open`，其他系统使用 `xdg-open`。程序正常输出只显示不含密钥的本地地址。无法调用默认浏览器时显示完整手动链接；系统已接受打开请求但没有出现网页时，也可用以下方式取得手动链接：

```powershell
go run ./agent/cmd/agent -open-browser=false
```

自动化测试、无图形环境和远程终端也使用此参数。Agent仍只允许回环监听；每次启动生成的连接密钥、HttpOnly会话、Host/Origin、写操作CSRF检查继续保留。自动打开改变使用方式，不开放匿名本地API；密钥保留在URL片段中，不进入服务器请求路径，也不发送给远程8182或图片CDN。

入口见 [main.go](../client/agent/cmd/agent/main.go)、[startup.go](../client/agent/cmd/agent/startup.go)、[Windows打开器](../client/agent/cmd/agent/browser_windows.go) 和 [其他系统打开器](../client/agent/cmd/agent/browser_other.go)。Windows使用系统 `ShellExecuteW` 的默认URL处理及COM初始化，返回值处理参考 [微软文档](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shellexecutew)。

## 图片数据链路

既有列表JSON不包含头像/封面字段，但历史快照已有 UP 的 `card.face`、视频的 `pic`。Agent按目标和版本读取既有快照，返回经过来源检查的URL，浏览器再加载图片。库卡片、总览小列表和详情页使用同一图片加载器；视频卡片和详情采用横向封面，UP采用圆形头像。

```text
+-----------------------------+
| browser: lazy image loader  |
+-----------------------------+
              | local image lookup
              v
+-----------------------------+
| Agent: versioned URL cache  |
+-----------------------------+
              | existing snapshot JSON
              v
+-----------------------------+
| 8182: recorded snapshots    |
+-----------------------------+

browser -- HTTPS image URL --> Bilibili image CDN
```

本地新增 `GET /api/images/{kind}/{id}?seq=1`，`kind`为 `creator/video`，标识按既有UID/BV规则校验。返回 `url`、`seq`、`available`、`cached`，不返回整份快照，也不代理图片文件。所有请求沿用原本地会话和来源检查。当前本地共25个API，清单见 [Agent说明](../client/agent/README.md#api-接口)。

已知版本通常只需一次既有详情请求；`seq=0`先读取当前版本，再读取对应快照。选中详情快照也会预热该版本的图片地址缓存。读取仅针对服务器已保存记录，不触发平台采集或公共跟踪。

## 缓存与边界

- 地址缓存存于工作目录 `library.json` 的 `images`，随目录迁移，最多2000项，按解析时间淘汰旧项；按目标/版本区分，更换服务器清空并拒绝旧请求的结果。缓存保存失败时仍可展示本次已取得的URL，原内存/文件状态不替换。
- 明确版本复用已解析结果直到淘汰；`seq=0`最多缓存15分钟，新版本使用新缓存键。快照没有图片时保留占位，不使用作者头像冒充视频封面。
- 浏览器按可见区域懒加载，最多4个图片元数据请求；Agent也最多并行4个远程图片解析，相同目标/版本请求合并。临时元数据错误在Agent退避1分钟，浏览器短暂退避；图片本身加载失败在当前页面暂缓重试1分钟并保留占位。
- 仅允许 `hdslb.com`、`biliimg.com`及其子域名，地址统一HTTPS，拒绝凭据、端口及其他协议。CSP只增加这些图片来源，脚本、API连接仍限同源。图片请求设置 `no-referrer`，不转发Agent会话或平台Cookie。
- 保存的是**图片地址**，尚未归档图片文件。源站/CDN地址失效后占位仍可显示，但不能保证历史图片永久可用；持久保存图片需要另行实现文件下载与版本关联。

## 模块与验证

- [remote/image.go](../client/agent/internal/remote/image.go)：快照图片字段与URL规范化；[测试](../client/agent/internal/remote/image_test.go) 覆盖域名、协议与凭据边界。
- [library/images.go](../client/agent/internal/library/images.go)：持久地址索引、并发、去重、退避与切服隔离；[测试](../client/agent/internal/library/images_test.go) 覆盖版本、重启、并发去重及错误。
- [httpapi/library.go](../client/agent/internal/httpapi/library.go)：受保护的图片地址API；[测试](../client/agent/internal/httpapi/images_test.go) 验证认证、参数及CSP。
- [images.js](../client/frontend/images.js)：可见区域加载、请求合并及图片失败占位，供 [cards.js](../client/frontend/cards.js)、[detail.js](../client/frontend/detail.js)、[library.js](../client/frontend/library.js) 使用；[前端测试](../client/frontend/images.test.mjs) 验证地址与快照提取。
- [启动测试](../client/agent/cmd/agent/startup_test.go) 使用替代浏览器启动器验证真实监听、会话交换、禁用与失败回退，避免测试弹出桌面窗口。Go测试/vet、Windows构建及Linux/macOS交叉构建通过。
- Edge真实Agent联调使用本地模拟8182/CDN，覆盖自动会话、密钥片段移除、头像/封面在各页面显示、缓存重启、离线地址复用、404/非法来源占位、4并发、无Cookie/Referer泄漏及390像素布局；原卡片/详情与135条扫描回归通过。未使用真实Cookie，不表示已部署远端。

其他客户端行为见 [卡片与详情](client-catalog.md)、[工作台说明](client-workspace.md) 和 [架构导航](architecture.md#module-navigation)。
