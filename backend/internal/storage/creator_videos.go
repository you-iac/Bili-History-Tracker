// creator_videos.go 提供作者视频的稳定续页与批量本地状态，不请求平台。
// 读取位置不依赖关注集合大小，添加过程中不会因结果缩短而跳项。
// 设计说明：docs/video-discovery.md。
package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bili-history-tracker/backend/internal/source"
)

// CreatorVideoRecord 是本地成功观测及当前管理员关注状态。
// 当前 Subscribed 映射全局 Tracking；未来用户关系应独立计算。
type CreatorVideoRecord struct {
	source.CreatorVideo
	Recorded   bool       `json:"recorded"`
	Tracking   bool       `json:"tracking"`
	Subscribed bool       `json:"subscribed"`
	Versions   int64      `json:"versions"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// CreatorVideos 按 video_id 稳定续页，包含已跟踪项，由用例决定展示范围。
func (s *Store) CreatorVideos(ctx context.Context, platform, creator, after string, limit int) ([]CreatorVideoRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `SELECT video_id FROM videos
WHERE platform=? AND creator_id=? AND video_id>? ORDER BY video_id LIMIT ?`, platform, creator, after, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	states, err := s.VideoStates(ctx, platform, ids)
	if err != nil {
		return nil, err
	}
	items := make([]CreatorVideoRecord, 0, len(ids))
	for _, id := range ids {
		items = append(items, states[id])
	}
	return items, nil
}

// VideoStates 一次读取一批 ID 的内容和启用目标；不逐条查询，缺失内容也保留目标状态。
func (s *Store) VideoStates(ctx context.Context, platform string, ids []string) (map[string]CreatorVideoRecord, error) {
	result := make(map[string]CreatorVideoRecord, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > 200 {
		return nil, fmt.Errorf("too many video IDs")
	}
	args := []any{platform}
	for _, id := range ids {
		result[id] = CreatorVideoRecord{CreatorVideo: source.CreatorVideo{ID: id}}
		args = append(args, id)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.QueryContext(ctx, `SELECT video_id,title,published_at,duration_seconds,view_count,current_seq,last_observed_at
FROM videos WHERE platform=? AND video_id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item CreatorVideoRecord
		var published *int64
		var observed int64
		if err := rows.Scan(&item.ID, &item.Title, &published, &item.DurationSeconds, &item.ViewCount, &item.Versions, &observed); err != nil {
			rows.Close()
			return nil, err
		}
		item.Recorded = true
		at := time.UnixMilli(observed).UTC()
		item.ObservedAt = &at
		if published != nil {
			at := time.UnixMilli(*published).UTC()
			item.PublishedAt = &at
		}
		result[item.ID] = item
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT target_id FROM tracking_targets WHERE platform=? AND target_type='video' AND enabled=1 AND target_id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		item := result[id]
		item.Tracking = true
		item.Subscribed = true
		result[id] = item
	}
	return result, rows.Err()
}
