// Package storage 负责 SQLite 持久化：打开数据库、执行 schema 迁移、
// 按 docs/database.md 的五表设计读写当前索引、内容版本、指标采样与调度目标。
//
// 输入：tracker 传入的 source.Video / CreatorProfile 及内容指纹；admin 传入的
// 目标增删改查参数和详情读取请求。
// 输出：SaveResult、VideoDetail / CreatorDetail、Target 列表；最终写入 SQLite 文件。
// 它不认识：B站端点与 HTTP 调用、调度循环，也不决定"多久查一次"（间隔由调用方传入）；
// 例外是 fingerprint.go 目前按 B站 data 的字段做内容投影，这是已知的平台耦合点。
//
// 上下游关系：
//
//	[上游] tracker（写内容） / admin（读详情、增删目标） / cmd/server（打开库）
//	          │ source.Video、CreatorProfile、Target
//	          ▼
//	[当前] ┌─────────────┐
//	       │   storage   │
//	       └──────┬──────┘
//	              │ database/sql
//	              ▼
//	[下游] SQLite 文件（单写者连接、WAL）
//
// 本包只处理数据落库与指纹计算，不发起网络请求；查询与调度由 tracker 包协调。
// 数据库连接固定为单写者，读取也在同一连接池内串行，避免 SQLITE_BUSY 抖动。
package storage
