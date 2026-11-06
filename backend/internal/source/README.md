# source 模块

平台无关的查询契约：统一数据结构、能力接口、错误分类，并按平台名分发。

> 与 [source.go](source.go) 的包注释保持一致；架构总览见 [docs/architecture.md](../../../docs/architecture.md)。

## 子模块协作

```text
tracker / httpapi
      │ 平台名 + 目标 ID + context
      ▼
   Service ──lookup──► Provider（接口）
      │                  ├─ CreatorProvider（可选）
      │                  └─ VideoLister（可选）
      ▼
 Video / CreatorProfile / CreatorVideo / Error
      │
      ▼
 provider/bilibili（实现）
```

`Service` 只负责能力分发；数据结构和 `Error` 由调用方与适配器共用。

## 输入 / 输出 / 边界

- **输入**：平台名、目标 ID、context。
- **输出**：`Video`、`CreatorProfile`、`CreatorVideo` 或 `*Error`。
- **它不认识**：SQLite、HTTP 路由、调度间隔、B站字段格式。

## 关键约束

- 注册表构造后不可变；空平台名或重复平台直接报错。
- 平台 ID 格式由 Provider 校验，本包只判空。
