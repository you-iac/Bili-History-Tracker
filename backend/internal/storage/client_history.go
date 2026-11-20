// client_history.go 为本地优先客户端提供按 ID 限定的采样、历史头和资料兜底。
// 不进入目录聚合、投稿汇总或上游采集；接口边界见 docs/local-first-metadata.md。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// HistoryHead 只读取版本指针和观测时间，作者占位行保留 Seq=0。
func (s *Store) HistoryHead(ctx context.Context, platform, kind, id string) (ContentItem, error) {
	i := ContentItem{Kind: kind, ID: id, Recorded: true}
	query := "SELECT current_seq,last_observed_at FROM videos WHERE platform=? AND video_id=?"
	if kind == "creator" {
		query = "SELECT COALESCE(current_profile_seq,0),COALESCE(last_profile_observed_at,0) FROM creators WHERE platform=? AND creator_id=?"
	} else if kind != "video" {
		return ContentItem{}, errors.New("invalid history kind")
	}
	err := s.db.QueryRowContext(ctx, query, platform, id).Scan(&i.Seq, &i.ObservedAt)
	return i, err
}

func batchIDs(ids []string) (string, []any, error) {
	if len(ids) == 0 || len(ids) > 24 {
		return "", nil, errors.New("batch requires 1 to 24 IDs")
	}
	seen, args := map[string]bool{}, make([]any, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return "", nil, errors.New("empty or duplicate batch ID")
		}
		seen[id] = true
		args = append(args, id)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args, nil
}

// MetricsBatch 一次查询指定目标的采样；每个目标最多保留最新5000点，缺失不补零。
func (s *Store) MetricsBatch(ctx context.Context, platform, kind string, ids []string, days int, now time.Time) (map[string]MetricSeries, error) {
	marks, selected, err := batchIDs(ids)
	if err != nil {
		return nil, err
	}
	metric := "view"
	if kind == "creator" {
		metric = "follower"
	} else if kind != "video" {
		return nil, errors.New("invalid metric kind")
	}
	if days != 7 {
		return nil, errors.New("batch metrics only support 7 days")
	}
	args := []any{platform, kind, metric, now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli(), now.UnixMilli()}
	args = append(args, selected...)
	rows, err := s.db.QueryContext(ctx, `WITH selected AS (
SELECT subject_id,sampled_at,value,ROW_NUMBER() OVER(PARTITION BY subject_id ORDER BY sampled_at DESC) n
FROM metric_samples WHERE platform=? AND subject_type=? AND metric=? AND sampled_at>=? AND sampled_at<=?
AND subject_id IN (`+marks+`)) SELECT subject_id,sampled_at,value FROM selected WHERE n<=5001 ORDER BY subject_id,sampled_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]MetricSeries, len(ids))
	for _, id := range ids {
		result[id] = MetricSeries{Points: []MetricPoint{}}
	}
	for rows.Next() {
		var id string
		var p MetricPoint
		if err := rows.Scan(&id, &p.At, &p.Value); err != nil {
			return nil, err
		}
		series := result[id]
		if len(series.Points) < 5000 {
			series.Points = append(series.Points, p)
		} else {
			series.Truncated = true
		}
		result[id] = series
	}
	for id, series := range result {
		for l, r := 0, len(series.Points)-1; l < r; l, r = l+1, r-1 {
			series.Points[l], series.Points[r] = series.Points[r], series.Points[l]
		}
		result[id] = series
	}
	return result, rows.Err()
}

// MetadataBatch 只为本地失败的目标读取当前资料，无计数、历史指标或任务/投稿聚合。
func (s *Store) MetadataBatch(ctx context.Context, platform, kind string, ids []string) (map[string]ContentItem, error) {
	marks, selected, err := batchIDs(ids)
	if err != nil {
		return nil, err
	}
	query := `SELECT v.video_id,v.title,COALESCE(json_extract(x.payload,'$.pic'),''),v.creator_id,c.name,v.view_count,
COALESCE(v.published_at,0),v.last_observed_at,v.current_seq
FROM videos v JOIN creators c ON c.platform=v.platform AND c.creator_id=v.creator_id
LEFT JOIN video_versions x ON x.platform=v.platform AND x.video_id=v.video_id AND x.seq=v.current_seq
WHERE v.platform=? AND v.video_id IN (` + marks + `)`
	if kind == "creator" {
		query = `SELECT c.creator_id,c.name,COALESCE(json_extract(x.payload,'$.card.face'),''),'','',c.follower_count,
0,COALESCE(c.last_profile_observed_at,0),COALESCE(c.current_profile_seq,0)
FROM creators c LEFT JOIN creator_versions x ON x.platform=c.platform AND x.creator_id=c.creator_id AND x.seq=c.current_profile_seq
WHERE c.platform=? AND c.creator_id IN (` + marks + `)`
	} else if kind != "video" {
		return nil, errors.New("invalid metadata kind")
	}
	rows, err := s.db.QueryContext(ctx, query, append([]any{platform}, selected...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]ContentItem, len(ids))
	for rows.Next() {
		i := ContentItem{Kind: kind, Recorded: true}
		var metric sql.NullInt64
		if err := rows.Scan(&i.ID, &i.Name, &i.Cover, &i.OwnerID, &i.OwnerName, &metric, &i.PublishedAt, &i.ObservedAt, &i.Seq); err != nil {
			return nil, err
		}
		if metric.Valid {
			i.Metric = &metric.Int64
		}
		result[i.ID] = i
	}
	return result, rows.Err()
}
