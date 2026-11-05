// Package storage 负责 SQLite 持久化：打开数据库、执行 schema 迁移、
// 按 docs/database.md 的五表设计读写当前索引、内容版本、指标采样与调度目标。
//
// 本包只处理数据落库与指纹计算，不发起网络请求；查询与调度由 tracker 包协调。
// 数据库连接固定为单写者，读取也在同一连接池内串行，避免 SQLITE_BUSY 抖动。
package storage
