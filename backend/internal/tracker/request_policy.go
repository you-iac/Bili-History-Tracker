// request_policy.go 定义显式申请的阶段预算与错误链，不用请求取消代替数据库原子性。
// 查询遵守请求取消；有效资料进入保存后使用独立短预算。见 docs/request-stages.md。
package tracker

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

// RequestBudgets 是单目标各阶段的预设；批量接纳和最终确认由 publicapi 约束。
type RequestBudgets struct {
	Check, Query, Persist, Target time.Duration
}

// DefaultRequestBudgets 返回独立副本，测试可注入短预算，不修改全局时钟。
func DefaultRequestBudgets() RequestBudgets {
	return RequestBudgets{Check: 2 * time.Second, Query: 12 * time.Second, Persist: 5 * time.Second, Target: 3 * time.Second}
}

// RequestError 保留阶段、底层原因和写入结果是否未知；errors.Is 可继续识别取消或存储故障。
// Outcome 为 not_applied 表示尚未开始写入，为 unknown 表示可能已有部分提交，不能补偿删除。
type RequestError struct {
	Stage, Outcome string
	Cause          error
}

func (e *RequestError) Error() string { return e.Stage + ": " + e.Cause.Error() }
func (e *RequestError) Unwrap() error { return e.Cause }

// requestStore 只包含显式申请所需的存储能力，可用包装器确定性验证事务中断和部分提交。
type requestStore interface {
	TrackingTarget(context.Context, string, string, string) (storage.Target, bool, error)
	SaveCreator(context.Context, source.CreatorProfile, storage.Fingerprint) (storage.SaveResult, error)
	SaveVideo(context.Context, source.Video, storage.Fingerprint) (storage.SaveResult, error)
	RecordMetric(context.Context, string, string, string, string, time.Time, int64) error
	AddRequestedTarget(context.Context, storage.Target, time.Time) (bool, error)
	RecordTargetResult(context.Context, storage.Target, string, time.Time) error
}

func requestStage(ctx context.Context, timeout time.Duration, stage, outcome string, work func(context.Context) error) error {
	phase, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := phase.Err(); err != nil {
		return &RequestError{Stage: stage, Outcome: outcome, Cause: err}
	}
	if err := work(phase); err != nil {
		// database/sql 会在取消后自动回滚事务，Commit 可能只返回 ErrTxDone。
		// 仅此明确的事务结束错误补充取消原因，不把数据库关闭等真实故障覆盖成超时。
		if errors.Is(err, sql.ErrTxDone) && phase.Err() != nil {
			err = errors.Join(err, phase.Err())
		}
		// 不因返回时 context 恰好过期而覆盖不相关的数据库故障；只依赖错误链。
		return &RequestError{Stage: stage, Outcome: outcome, Cause: err}
	}
	return nil
}
