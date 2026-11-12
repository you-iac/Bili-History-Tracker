// target_lookup.go 为客户端接入提供精确目标读取及只新增的调度写入，避免重复申请改变调度。
// 设计与调用关系见 docs/client-library.md。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TrackingTarget 区分不存在与已禁用；不能用 HasTarget 推断是否正在跟踪。
func (s *Store) TrackingTarget(ctx context.Context, platform, kind, id string) (Target, bool, error) {
	t := Target{Platform: platform, TargetType: kind, TargetID: id}
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT interval_seconds,next_run_at,last_run_at,enabled,created_at FROM tracking_targets WHERE platform=? AND target_type=? AND target_id=?`, platform, kind, id).Scan(&t.IntervalSeconds, &t.NextRunAt, &last, &t.Enabled, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	if last.Valid {
		t.LastRunAt = &last.Int64
	}
	return t, err == nil, err
}

// AddRequestedTarget 只插入新目标，冲突时不修改既有间隔、启停状态或下次运行时间。
func (s *Store) AddRequestedTarget(ctx context.Context, target Target, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO tracking_targets (platform,target_type,target_id,interval_seconds,next_run_at,last_run_at,created_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT (platform,target_type,target_id) DO NOTHING`, target.Platform, target.TargetType, target.TargetID, target.IntervalSeconds, now.Add(time.Duration(target.IntervalSeconds)*time.Second).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
