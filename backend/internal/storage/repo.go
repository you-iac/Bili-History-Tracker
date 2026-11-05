// repo.go 实现当前索引、内容版本与调度目标的读写。
// 写入遵循 docs/database.md：先判断是否迟到，再在事务内写版本和当前表；
// 内容未变化时不写版本，只推进计数与观测时间。

package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"bili-history-tracker/backend/internal/source"
)

// SaveResult 描述一次成功观测对数据库的影响。
type SaveResult struct {
	Created      bool  // 是否首次插入当前表
	VersionAdded bool  // 是否新增了内容版本
	Seq          int64 // 写入后的当前版本序号
}

// Target 是调度目标及其展示信息。
type Target struct {
	Platform        string
	TargetType      string
	TargetID        string
	IntervalSeconds int64
	NextRunAt       int64
	LastRunAt       *int64
	Enabled         bool
	CreatedAt       int64
	DisplayName     string
	VersionCount    int64
}

// VideoDetail 是详情读取结果：当前表字段加最新版本的 payload。
type VideoDetail struct {
	VideoID        string
	Title          string
	ViewCount      *int64
	PublishedAt    *int64
	FirstSeenAt    int64
	LastObservedAt int64
	CurrentSeq     int64
	Payload        []byte
	SourceURL      string
	ObservedAt     int64
}

// CreatorDetail 是作者详情：当前表字段加最新完整资料版本的 payload。
// 只有视频引用、没有完整资料时 Payload 为空。
type CreatorDetail struct {
	CreatorID         string
	Name              string
	FollowerCount     *int64
	FirstSeenAt       int64
	LastReferencedAt  int64
	ProfileObservedAt *int64
	CurrentSeq        *int64
	Payload           []byte
	SourceURL         string
	ObservedAt        *int64
}

// SaveVideo 写入一次成功的视频观测。迟到响应不产生任何写入。
func (s *Store) SaveVideo(ctx context.Context, video source.Video, fingerprint Fingerprint) (SaveResult, error) {
	if len(video.Raw) == 0 {
		return SaveResult{}, errors.New("video payload is empty")
	}
	if video.ObservedAt.IsZero() {
		video.ObservedAt = time.Now().UTC()
	}
	observedAt := video.ObservedAt.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, fmt.Errorf("begin video transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result SaveResult
	var currentHash []byte
	var currentSeq, lastObserved int64
	scanErr := tx.QueryRowContext(ctx,
		`SELECT current_hash, current_seq, last_observed_at FROM videos WHERE platform = ? AND video_id = ?`,
		video.Platform, video.ID).Scan(&currentHash, &currentSeq, &lastObserved)
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		result.Created = true
		result.VersionAdded = true
		result.Seq = 1
		if err := upsertCreatorReference(ctx, tx, video, observedAt); err != nil {
			return SaveResult{}, err
		}
		if err := insertVideo(ctx, tx, video, fingerprint, observedAt); err != nil {
			return SaveResult{}, err
		}
		if err := insertVideoVersion(ctx, tx, video, fingerprint, 1, observedAt); err != nil {
			return SaveResult{}, err
		}
	case scanErr != nil:
		return SaveResult{}, fmt.Errorf("read video row: %w", scanErr)
	default:
		if observedAt < lastObserved {
			// Late observation: discard entirely, including the creator touch.
			return SaveResult{}, nil
		}
		result.Seq = currentSeq
		if err := upsertCreatorReference(ctx, tx, video, observedAt); err != nil {
			return SaveResult{}, err
		}
		if !bytes.Equal(currentHash, fingerprint[:]) {
			result.Seq = currentSeq + 1
			result.VersionAdded = true
			if err := insertVideoVersion(ctx, tx, video, fingerprint, result.Seq, observedAt); err != nil {
				return SaveResult{}, err
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE videos SET title = ?, published_at = ?, duration_seconds = ?, view_count = ?,
    last_observed_at = ?, current_hash = ?, current_seq = ?
WHERE platform = ? AND video_id = ?`,
				video.Title, nullableTime(video.PublishedAt), video.DurationSeconds, nullableInt64(video.ViewCount),
				observedAt, fingerprint[:], result.Seq, video.Platform, video.ID); err != nil {
				return SaveResult{}, fmt.Errorf("update video content: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
UPDATE videos SET view_count = ?, last_observed_at = ?
WHERE platform = ? AND video_id = ?`,
				nullableInt64(video.ViewCount), observedAt, video.Platform, video.ID); err != nil {
				return SaveResult{}, fmt.Errorf("refresh video counters: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, fmt.Errorf("commit video transaction: %w", err)
	}
	return result, nil
}

// SaveCreator 写入一次成功的完整资料观测。迟到响应不产生任何写入。
func (s *Store) SaveCreator(ctx context.Context, profile source.CreatorProfile, fingerprint Fingerprint) (SaveResult, error) {
	if len(profile.Raw) == 0 {
		return SaveResult{}, errors.New("creator payload is empty")
	}
	if profile.ObservedAt.IsZero() {
		profile.ObservedAt = time.Now().UTC()
	}
	observedAt := profile.ObservedAt.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, fmt.Errorf("begin creator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result SaveResult
	var profileObserved sql.NullInt64
	var currentHash []byte
	var currentSeq sql.NullInt64
	scanErr := tx.QueryRowContext(ctx,
		`SELECT last_profile_observed_at, current_profile_hash, current_profile_seq
		 FROM creators WHERE platform = ? AND creator_id = ?`,
		profile.Platform, profile.ID).Scan(&profileObserved, &currentHash, &currentSeq)
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		result.Created = true
		result.VersionAdded = true
		result.Seq = 1
		if _, err := tx.ExecContext(ctx, `
INSERT INTO creators (platform, creator_id, name, follower_count, first_seen_at,
    last_referenced_at, last_profile_observed_at, current_profile_hash, current_profile_seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			profile.Platform, profile.ID, profile.Name, nullableInt64(profile.FollowerCount),
			observedAt, observedAt, observedAt, fingerprint[:]); err != nil {
			return SaveResult{}, fmt.Errorf("insert creator: %w", err)
		}
		if err := insertCreatorVersion(ctx, tx, profile, fingerprint, 1, observedAt); err != nil {
			return SaveResult{}, err
		}
	case scanErr != nil:
		return SaveResult{}, fmt.Errorf("read creator row: %w", scanErr)
	default:
		if profileObserved.Valid && observedAt < profileObserved.Int64 {
			return SaveResult{}, nil
		}
		result.Seq = currentSeq.Int64
		if !bytes.Equal(currentHash, fingerprint[:]) {
			result.Seq = currentSeq.Int64 + 1
			result.VersionAdded = true
			if err := insertCreatorVersion(ctx, tx, profile, fingerprint, result.Seq, observedAt); err != nil {
				return SaveResult{}, err
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE creators SET name = ?, follower_count = ?, last_profile_observed_at = ?,
    current_profile_hash = ?, current_profile_seq = ?, last_referenced_at = ?
WHERE platform = ? AND creator_id = ?`,
				profile.Name, nullableInt64(profile.FollowerCount), observedAt,
				fingerprint[:], result.Seq, observedAt, profile.Platform, profile.ID); err != nil {
				return SaveResult{}, fmt.Errorf("update creator content: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
UPDATE creators SET follower_count = ?, last_profile_observed_at = ?, last_referenced_at = ?
WHERE platform = ? AND creator_id = ?`,
				nullableInt64(profile.FollowerCount), observedAt, observedAt,
				profile.Platform, profile.ID); err != nil {
				return SaveResult{}, fmt.Errorf("refresh creator counters: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, fmt.Errorf("commit creator transaction: %w", err)
	}
	return result, nil
}

// RecordMetric 在采样窗口内写入或覆盖一个指标值。
func (s *Store) RecordMetric(ctx context.Context, platform, subjectType, subjectID, metric string, sampledAt time.Time, value int64) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO metric_samples (platform, subject_type, subject_id, metric, sampled_at, value)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, subject_type, subject_id, metric, sampled_at)
DO UPDATE SET value = excluded.value`,
		platform, subjectType, subjectID, metric, sampledAt.UnixMilli(), value)
	if err != nil {
		return fmt.Errorf("record metric: %w", err)
	}
	return nil
}

// LatestVideo 读取视频当前字段与最新版本的 payload。
func (s *Store) LatestVideo(ctx context.Context, platform, videoID string) (VideoDetail, error) {
	var detail VideoDetail
	var viewCount, publishedAt sql.NullInt64
	var payload string
	err := s.db.QueryRowContext(ctx, `
SELECT v.video_id, v.title, v.view_count, v.published_at, v.first_seen_at, v.last_observed_at,
       v.current_seq, x.payload, x.source_url, x.observed_at
FROM videos AS v
JOIN video_versions AS x
  ON x.platform = v.platform AND x.video_id = v.video_id AND x.seq = v.current_seq
WHERE v.platform = ? AND v.video_id = ?`, platform, videoID).Scan(
		&detail.VideoID, &detail.Title, &viewCount, &publishedAt, &detail.FirstSeenAt,
		&detail.LastObservedAt, &detail.CurrentSeq, &payload, &detail.SourceURL, &detail.ObservedAt)
	if err != nil {
		return VideoDetail{}, err
	}
	if viewCount.Valid {
		detail.ViewCount = &viewCount.Int64
	}
	if publishedAt.Valid {
		detail.PublishedAt = &publishedAt.Int64
	}
	detail.Payload = []byte(payload)
	return detail, nil
}

// LatestCreator 读取作者当前字段与最新完整资料的 payload。
func (s *Store) LatestCreator(ctx context.Context, platform, creatorID string) (CreatorDetail, error) {
	var detail CreatorDetail
	var follower, profileObserved, currentSeq, observedAt sql.NullInt64
	var payload, sourceURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT c.creator_id, c.name, c.follower_count, c.first_seen_at, c.last_referenced_at,
       c.last_profile_observed_at, c.current_profile_seq,
       x.payload, x.source_url, x.observed_at
FROM creators AS c
LEFT JOIN creator_versions AS x
  ON x.platform = c.platform AND x.creator_id = c.creator_id AND x.seq = c.current_profile_seq
WHERE c.platform = ? AND c.creator_id = ?`, platform, creatorID).Scan(
		&detail.CreatorID, &detail.Name, &follower, &detail.FirstSeenAt, &detail.LastReferencedAt,
		&profileObserved, &currentSeq, &payload, &sourceURL, &observedAt)
	if err != nil {
		return CreatorDetail{}, err
	}
	if follower.Valid {
		detail.FollowerCount = &follower.Int64
	}
	if profileObserved.Valid {
		detail.ProfileObservedAt = &profileObserved.Int64
	}
	if currentSeq.Valid {
		detail.CurrentSeq = &currentSeq.Int64
	}
	if observedAt.Valid {
		detail.ObservedAt = &observedAt.Int64
	}
	if payload.Valid {
		detail.Payload = []byte(payload.String)
	}
	detail.SourceURL = sourceURL.String
	return detail, nil
}

// VideoVersionCount 返回某个视频已保存的版本数。
func (s *Store) VideoVersionCount(ctx context.Context, platform, videoID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM video_versions WHERE platform = ? AND video_id = ?`,
		platform, videoID).Scan(&count)
	return count, err
}

// CreatorVersionCount 返回某个作者已保存的版本数。
func (s *Store) CreatorVersionCount(ctx context.Context, platform, creatorID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM creator_versions WHERE platform = ? AND creator_id = ?`,
		platform, creatorID).Scan(&count)
	return count, err
}

// AddTarget 创建或更新一个调度目标，并立即安排一次运行。
func (s *Store) AddTarget(ctx context.Context, platform, targetType, targetID string, interval time.Duration, now time.Time) error {
	if interval < time.Minute {
		interval = time.Minute
	}
	nowMs := now.UnixMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tracking_targets (platform, target_type, target_id, interval_seconds, next_run_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, target_type, target_id) DO UPDATE SET
    interval_seconds = excluded.interval_seconds,
    next_run_at = min(tracking_targets.next_run_at, excluded.next_run_at),
    enabled = 1`,
		platform, targetType, targetID, int64(interval.Seconds()), nowMs, nowMs)
	if err != nil {
		return fmt.Errorf("add tracking target: %w", err)
	}
	return nil
}

// RemoveTarget 删除调度目标，不删除已经保存的内容历史。
func (s *Store) RemoveTarget(ctx context.Context, platform, targetType, targetID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM tracking_targets WHERE platform = ? AND target_type = ? AND target_id = ?`,
		platform, targetType, targetID)
	return err
}

// HasTarget 报告某个目标是否已经在调度表中。
func (s *Store) HasTarget(ctx context.Context, platform, targetType, targetID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM tracking_targets WHERE platform = ? AND target_type = ? AND target_id = ?`,
		platform, targetType, targetID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SetTargetInterval 只更新调度间隔，保留下次运行时间不早于当前计划。
func (s *Store) SetTargetInterval(ctx context.Context, platform, targetType, targetID string, interval time.Duration) error {
	if interval < time.Minute {
		interval = time.Minute
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE tracking_targets SET interval_seconds = ? WHERE platform = ? AND target_type = ? AND target_id = ?`,
		int64(interval.Seconds()), platform, targetType, targetID)
	return err
}

// DueTargets 返回已经到达运行时间的启用目标。
func (s *Store) DueTargets(ctx context.Context, now time.Time, limit int) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT platform, target_type, target_id, interval_seconds, next_run_at, last_run_at, enabled, created_at
FROM tracking_targets
WHERE enabled = 1 AND next_run_at <= ?
ORDER BY next_run_at
LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// ListTargets 返回全部调度目标及展示名称。
func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.platform, t.target_type, t.target_id, t.interval_seconds, t.next_run_at,
       t.last_run_at, t.enabled, t.created_at,
       COALESCE(c.name, v.title, '') AS display_name,
       CASE WHEN t.target_type = 'video'
            THEN (SELECT COUNT(*) FROM video_versions x
                  WHERE x.platform = t.platform AND x.video_id = t.target_id)
            ELSE (SELECT COUNT(*) FROM creator_versions x
                  WHERE x.platform = t.platform AND x.creator_id = t.target_id)
       END AS version_count
FROM tracking_targets AS t
LEFT JOIN creators AS c
  ON t.target_type = 'creator' AND c.platform = t.platform AND c.creator_id = t.target_id
LEFT JOIN videos AS v
  ON t.target_type = 'video' AND v.platform = t.platform AND v.video_id = t.target_id
ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]Target, 0)
	for rows.Next() {
		var target Target
		var lastRun sql.NullInt64
		var enabled int
		if err := rows.Scan(&target.Platform, &target.TargetType, &target.TargetID,
			&target.IntervalSeconds, &target.NextRunAt, &lastRun, &enabled,
			&target.CreatedAt, &target.DisplayName, &target.VersionCount); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			target.LastRunAt = &lastRun.Int64
		}
		target.Enabled = enabled == 1
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// MarkTargetRun 记录本次运行并安排下一次时间。
func (s *Store) MarkTargetRun(ctx context.Context, target Target, ranAt, nextRunAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE tracking_targets SET last_run_at = ?, next_run_at = ?
WHERE platform = ? AND target_type = ? AND target_id = ?`,
		ranAt.UnixMilli(), nextRunAt.UnixMilli(), target.Platform, target.TargetType, target.TargetID)
	return err
}

// Counts 返回各表行数，用于后台展示设计验证结果。
func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	tables := []string{"creators", "videos", "creator_versions", "video_versions", "metric_samples", "tracking_targets"}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}

func upsertCreatorReference(ctx context.Context, tx *sql.Tx, video source.Video, observedAt int64) error {
	name := video.Creator.Name
	if name == "" {
		name = video.Creator.ID
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO creators (platform, creator_id, name, first_seen_at, last_referenced_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (platform, creator_id) DO UPDATE SET
    last_referenced_at = max(creators.last_referenced_at, excluded.last_referenced_at)`,
		video.Platform, video.Creator.ID, name, observedAt, observedAt)
	if err != nil {
		return fmt.Errorf("upsert creator reference: %w", err)
	}
	return nil
}

func insertVideo(ctx context.Context, tx *sql.Tx, video source.Video, fingerprint Fingerprint, observedAt int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO videos (platform, video_id, creator_id, title, published_at, duration_seconds,
    view_count, first_seen_at, last_observed_at, current_hash, current_seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		video.Platform, video.ID, video.Creator.ID, video.Title, nullableTime(video.PublishedAt),
		video.DurationSeconds, nullableInt64(video.ViewCount), observedAt, observedAt, fingerprint[:])
	if err != nil {
		return fmt.Errorf("insert video: %w", err)
	}
	return nil
}

func insertVideoVersion(ctx context.Context, tx *sql.Tx, video source.Video, fingerprint Fingerprint, seq, observedAt int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO video_versions (platform, video_id, seq, observed_at, source_url,
    content_hash, hash_version, payload_encoding, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, 'json', ?)`,
		video.Platform, video.ID, seq, observedAt, video.SourceURL,
		fingerprint[:], FingerprintVersion, string(video.Raw))
	if err != nil {
		return fmt.Errorf("insert video version: %w", err)
	}
	return nil
}

func insertCreatorVersion(ctx context.Context, tx *sql.Tx, profile source.CreatorProfile, fingerprint Fingerprint, seq, observedAt int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO creator_versions (platform, creator_id, seq, observed_at, source_url,
    content_hash, hash_version, payload_encoding, payload)
VALUES (?, ?, ?, ?, ?, ?, ?, 'json', ?)`,
		profile.Platform, profile.ID, seq, observedAt, profile.SourceURL,
		fingerprint[:], FingerprintVersion, string(profile.Raw))
	if err != nil {
		return fmt.Errorf("insert creator version: %w", err)
	}
	return nil
}

func scanTargets(rows *sql.Rows) ([]Target, error) {
	targets := make([]Target, 0)
	for rows.Next() {
		var target Target
		var lastRun sql.NullInt64
		var enabled int
		if err := rows.Scan(&target.Platform, &target.TargetType, &target.TargetID,
			&target.IntervalSeconds, &target.NextRunAt, &lastRun, &enabled, &target.CreatedAt); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			target.LastRunAt = &lastRun.Int64
		}
		target.Enabled = enabled == 1
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UnixMilli()
}
