// detail_sources.go 按目标主键读取详情记录、任务和最后保存资料，不经过目录聚合。
// 普通路径不读取版本 payload；版本列表无总数查询。见 docs/client-detail-data-sources.md。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// DetailRecord 只描述服务器观测，不代表平台当前资料；Seq=0 不算成功记录。
type DetailRecord struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Seq           int64  `json:"seq"`
	ObservedAt    int64  `json:"observed_at"`
	Recorded      bool   `json:"recorded"`
	TrackingState string `json:"tracking_state"`
	Tracking      bool   `json:"tracking"`
	Status        string `json:"status"`
}

func detailTable(kind string) (table, id, seq, observed string, err error) {
	switch kind {
	case "creator":
		return "creators", "creator_id", "current_profile_seq", "last_profile_observed_at", nil
	case "video":
		return "videos", "video_id", "current_seq", "last_observed_at", nil
	default:
		return "", "", "", "", errors.New("invalid detail kind")
	}
}

// ReadDetailTarget 一次关联读取当前记录和真实任务，包括双方均不存在的情况。
// 以输入标识作为锚点，不能用作者占位、无记录或无任务作为额外目录探测的理由。
func (s *Store) ReadDetailTarget(ctx context.Context, platform, kind, id string, now time.Time) (DetailRecord, error) {
	table, column, seq, observed, err := detailTable(kind)
	if err != nil {
		return DetailRecord{}, err
	}
	r := DetailRecord{Kind: kind, ID: id, TrackingState: "absent", Status: "not_recorded"}
	var exists, enabled bool
	var interval, success, recovered int64
	var result string
	query := `SELECT COALESCE(c.` + seq + `,0), COALESCE(c.` + observed + `,0), t.target_id IS NOT NULL,
COALESCE(t.enabled,0),COALESCE(t.interval_seconds,0),COALESCE(t.last_success_at,c.` + observed + `,0),COALESCE(t.last_result,''),COALESCE(t.recovered_at,0)
FROM (SELECT ? platform, ? id) k LEFT JOIN ` + table + ` c ON c.platform=k.platform AND c.` + column + `=k.id
LEFT JOIN tracking_targets t ON t.platform=k.platform AND t.target_type=? AND t.target_id=k.id`
	err = s.db.QueryRowContext(ctx, query, platform, id, kind).Scan(&r.Seq, &r.ObservedAt, &exists, &enabled, &interval, &success, &result, &recovered)
	if err != nil {
		return DetailRecord{}, err
	}
	r.Recorded = r.Seq > 0
	if r.Recorded {
		r.Status = "unfollowed"
	}
	if exists {
		r.TrackingState = "paused"
		r.Status = "paused"
		if enabled {
			r.Tracking = true
			r.TrackingState = "active"
			r.Status = "ok"
			switch {
			case !r.Recorded:
				r.Status = "pending"
			case result != "" && result != "ok":
				r.Status = "error"
			case success == 0:
				r.Status = "pending"
			case now.UnixMilli()-success > (max(interval*2, 1800)+1800)*1000:
				r.Status = "stale"
			case recovered > 0:
				r.Status = "recovered"
			}
		}
	}
	return r, nil
}

// SavedContent 的计数与观测时间来自当前行，描述与图片来自同次查询绑定的当前版本。
type SavedContent struct {
	Item        ContentItem
	Description string
	SnapshotAt  int64
}

// ReadSavedContent 一次查询返回最后保存资料；不请求平台、不采集、不申请任务。
func (s *Store) ReadSavedContent(ctx context.Context, platform, kind, id string) (SavedContent, error) {
	var r SavedContent
	r.Item.Kind = kind
	r.Item.ID = id
	r.Item.Recorded = true
	var metric sql.NullInt64
	var encoding string
	var query string
	switch kind {
	case "creator":
		query = `SELECT c.name,c.follower_count,c.current_profile_seq,c.last_profile_observed_at,x.observed_at,x.payload_encoding,
COALESCE(json_extract(x.payload,'$.card.sign'),''),COALESCE(json_extract(x.payload,'$.card.face'),'')
FROM creators c JOIN creator_versions x ON x.platform=c.platform AND x.creator_id=c.creator_id AND x.seq=c.current_profile_seq
WHERE c.platform=? AND c.creator_id=?`
		err := s.db.QueryRowContext(ctx, query, platform, id).Scan(&r.Item.Name, &metric, &r.Item.Seq, &r.Item.ObservedAt, &r.SnapshotAt, &encoding, &r.Description, &r.Item.Cover)
		if err != nil {
			return r, err
		}
	case "video":
		query = `SELECT v.title,v.view_count,v.current_seq,v.last_observed_at,x.observed_at,x.payload_encoding,
COALESCE(json_extract(x.payload,'$.desc'),''),COALESCE(json_extract(x.payload,'$.pic'),''),v.creator_id,c.name,COALESCE(v.published_at,0)
FROM videos v JOIN video_versions x ON x.platform=v.platform AND x.video_id=v.video_id AND x.seq=v.current_seq
JOIN creators c ON c.platform=v.platform AND c.creator_id=v.creator_id WHERE v.platform=? AND v.video_id=?`
		err := s.db.QueryRowContext(ctx, query, platform, id).Scan(&r.Item.Name, &metric, &r.Item.Seq, &r.Item.ObservedAt, &r.SnapshotAt, &encoding, &r.Description, &r.Item.Cover, &r.Item.OwnerID, &r.Item.OwnerName, &r.Item.PublishedAt)
		if err != nil {
			return r, err
		}
	default:
		return r, errors.New("invalid detail kind")
	}
	if encoding != "json" {
		return r, errors.New("unsupported snapshot encoding")
	}
	if metric.Valid {
		r.Item.Metric = &metric.Int64
	}
	return r, nil
}

// DetailVersions 是游标页；没有 total，新增版本不会使旧页偏移。
type DetailVersions struct {
	Items      []VersionEntry `json:"items"`
	HasMore    bool           `json:"has_more"`
	NextBefore int64          `json:"next_before"`
}

// ReadDetailVersions 按 seq 递减、固定十条续页，只读指定目标的版本索引。
func (s *Store) ReadDetailVersions(ctx context.Context, platform, kind, id string, before int64) (DetailVersions, error) {
	r := DetailVersions{Items: []VersionEntry{}}
	table, column, _, _, err := detailTable(kind)
	if err != nil || before < 0 {
		return r, errors.New("invalid version cursor")
	}
	if table == "creators" {
		table = "creator_versions"
	} else {
		table = "video_versions"
	}
	query := "SELECT seq,observed_at FROM " + table + " WHERE platform=? AND " + column + "=?"
	args := []any{platform, id}
	if before > 0 {
		query += " AND seq<?"
		args = append(args, before)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY seq DESC LIMIT "+strconv.Itoa(11), args...)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		v := VersionEntry{Kind: kind, ID: id}
		if err = rows.Scan(&v.Seq, &v.At); err != nil {
			return r, err
		}
		r.Items = append(r.Items, v)
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	if len(r.Items) > 10 {
		r.HasMore = true
		r.Items = r.Items[:10]
		r.NextBefore = r.Items[9].Seq
	}
	return r, nil
}
