# provider 目录

平台适配器目录：每个子目录实现 `source` 定义的接口，把上游响应转换成统一结构。

## 子模块协作

```text
source.Service
      │ Provider / CreatorProvider / VideoLister
      ▼
 provider/
   └── bilibili/ ──► api.bilibili.com
         ├─ bilibili.go   GetVideo
         ├─ creator.go    GetCreator
         ├─ list.go       GetCreatorVideos
         └─ http.go       共用请求与校验
```

新增平台时复制这个目录结构，实现相同接口，再到 `cmd/server/main.go` 注册。

## 新增平台

1. 新建 `provider/<platform>/`；
2. 实现 `source.Provider` 的 `Platform()` / `GetVideo()`；
3. 按需实现 `CreatorProvider` / `VideoLister`；
4. 在 `cmd/server/main.go` 的 `source.NewService(...)` 中注册。

## 边界

- 适配器只做“请求 + 解析 + 转换”，不碰数据库、不决定调度。
- 平台 ID 校验留在适配器内；`source` 不假设 BV/UID 格式。
- `storage/fingerprint.go` 仍按 B站字段投影，新增平台要一并处理。
