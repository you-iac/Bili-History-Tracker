# Bili History Tracker

视频历史追踪项目。目前实现了服务端的最小视频元数据查询，不包含用户端、订阅调度和数据库。

需要 Go 1.22 或更高版本，无第三方依赖。

```powershell
cd backend
go run ./cmd/server
```

默认监听 `127.0.0.1:8080`，可通过环境变量 `BHT_ADDR` 修改地址。

```powershell
curl.exe http://127.0.0.1:8080/healthz
curl.exe http://127.0.0.1:8080/api/v1/platforms/bilibili/videos/BV17x411w7KC
```

目前仅接受完整 BV 号，区分大小写，不接受视频链接或 av 号。返回标题、简介、封面地址、UP 主、发布时间、时长、来源地址和观测时间；不请求播放地址或下载媒体。

```powershell
cd backend
go test ./...
go vet ./...
go build -o bin/server.exe ./cmd/server
```

查询适配器使用 B站网页元数据端点 `https://api.bilibili.com/x/web-interface/view`，属于实验性接入，并非具有稳定性承诺的开放平台 API。它不使用登录 Cookie，不重试或绕过访问限制；上游限制和失败会明确返回。实际接入范围与上线条件仍按 [PRD](docs/PRD.md) 确认。

当前查询结果不持久化，不能查询已消失视频的历史记录。元数据查询成功也不代表视频可以播放；查询不到不代表视频已被删除。

从 curl 到 B站再到 JSON 返回的执行链路、API 路由配置、`httpapi` 与 Provider 的职责及扩展方式见 [架构说明](docs/architecture.md)。
