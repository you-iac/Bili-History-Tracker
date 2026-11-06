// Package tracker 实现后台的循环查询：按调度表逐个查询作者资料和视频元数据，
// 把成功观测交给 storage 落库，并把每次查询的结果写入内存环形日志和日志文件。
//
// 输入：storage.Target（类型、ID、间隔；平台名当前由构造函数提供），以及
// Querier 接口返回的上游数据。
// 输出：以副作用为主——storage 落库、记录指标、推进调度表，Log 写日志；
// RunTarget / RunNow 不向调用方返回结果，失败只体现在日志里。
// 它不认识：B站 HTTP 细节、SQL 语句、HTML 页面。
//
// 上下游关系：
//
//	[上游] admin（RunNow） / scheduler（RunTarget）
//	          │ storage.Target
//	          ▼
//	[当前] ┌─────────────┐
//	       │   tracker   │
//	       └──┬───────┬──┘
//	          │       │
//	          ▼       ▼
//	[下游] source   storage / Log
//
// 本包不做状态推断：查询失败只记录错误，不删除或降级已有内容；作者合集发现
// 只补充新的视频目标，不代表作者全部投稿。调度串行执行，避免上游限流。
package tracker
