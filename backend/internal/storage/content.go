// content.go 按目标主键读取详情摘要，避免目录计数、混合类型和全库指标窗口。
// 字段与 Catalog 共用观测组装规则；边界与性能验证见 docs/detail-query-performance.md。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const videoContentItem = `SELECT 'video',v.video_id,v.title,v.creator_id,c.name,
v.view_count,v.current_seq,0,v.first_seen_at,v.last_observed_at,COALESCE(v.published_at,0),1,
v.view_count,NULL
FROM videos v JOIN creators c ON c.platform=v.platform AND c.creator_id=v.creator_id
WHERE v.platform=:platform AND v.video_id=:id`

const creatorContentItem = `SELECT 'creator',c.creator_id,c.name,'','',c.follower_count,
COALESCE(c.current_profile_seq,0),
(SELECT COUNT(*) FROM videos v WHERE v.platform=c.platform AND v.creator_id=c.creator_id),
c.first_seen_at,COALESCE(c.last_profile_observed_at,0),
COALESCE((SELECT MAX(v.published_at) FROM videos v WHERE v.platform=c.platform AND v.creator_id=c.creator_id),0),1,
(SELECT SUM(v.view_count) FROM videos v WHERE v.platform=c.platform AND v.creator_id=c.creator_id),
(SELECT COUNT(*) FROM videos v WHERE v.platform=c.platform AND v.creator_id=c.creator_id AND v.published_at>=:since AND v.published_at<=:now)
FROM creators c WHERE c.platform=:platform AND c.creator_id=:id`

// contentQuery 不接收外部 SQL；只在两种固定投影中选择，窗口使用目标主键前缀。
func contentQuery(kind string) (string, error) {
	item, metric := videoContentItem, "view"
	if kind == "creator" {
		item, metric = creatorContentItem, "follower"
	} else if kind != "video" {
		return "", errors.New("invalid content kind")
	}
	return `WITH items(kind,id,name,owner_id,owner_name,metric,seq,video_count,first_seen,observed,published,recorded,video_views,recent_videos) AS (` + item + `),
windows AS (
SELECT subject_type,subject_id,metric,MIN(sampled_at) first_at,MAX(sampled_at) last_at,COUNT(*) samples
FROM metric_samples WHERE platform=:platform AND subject_type=:kind AND subject_id=:id
AND metric='` + metric + `' AND sampled_at>=:since AND sampled_at<=:now
GROUP BY subject_type,subject_id,metric
) ` + contentEnrichmentCTE + `
SELECT p.*,COALESCE(CASE WHEN p.kind='video' THEN json_extract(v.payload,'$.pic') ELSE json_extract(c.payload,'$.card.face') END,'')
FROM enriched p
LEFT JOIN video_versions v ON p.kind='video' AND v.platform=:platform AND v.video_id=p.id AND v.seq=p.seq
LEFT JOIN creator_versions c ON p.kind='creator' AND c.platform=:platform AND c.creator_id=p.id AND c.seq=p.seq`, nil
}

// Content 仅查询指定平台、类型和 ID 的已有资料，不创建任务或获取上游数据。
// 没有资料返回 sql.ErrNoRows；视频引用形成的作者占位行仍返回 Seq=0。
// 指标和投稿统计与 Catalog 相同，但不执行目录总数、排序或分页。
func (s *Store) Content(ctx context.Context, platform, kind, id string, now time.Time) (ContentItem, error) {
	query, err := contentQuery(kind)
	if err != nil {
		return ContentItem{}, err
	}
	item, err := scanContent(s.db.QueryRowContext(ctx, query,
		sql.Named("platform", platform), sql.Named("kind", kind), sql.Named("id", id),
		sql.Named("since", now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli()),
		sql.Named("now", now.UnixMilli())), now)
	if err != nil {
		return ContentItem{}, err
	}
	return item, nil
}
