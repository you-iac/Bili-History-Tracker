// dashboard.go 为观察台提供分页内容视图、排行、动态和日指标查询。
// 仅从本地已记录数据聚合；列表只提取当前快照图片，不返回整份 payload。
// 数据口径与边界见 docs/dashboard.md；不发起上游请求。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContentItem 是页面读取模型；Metric/Growth 为 nil 表示未知或样本不足。
type ContentItem struct {
	Kind, ID, Name, Cover, OwnerID, OwnerName             string
	Metric, Growth                                        *int64
	Seq, VideoCount, FirstSeenAt, ObservedAt, PublishedAt int64
	Recorded, Following                                   bool
	IntervalSeconds, NextRunAt, LastRunAt, LastSuccessAt  int64
	LastResult, FailureReason, Status                     string
	Failures, LastFailureAt, RecoveredAt                  int64
	GrowthFrom, GrowthTo                                  int64
}

// CatalogFilter 约束读取范围；排序名由白名单解释，分页上限 48。
type CatalogFilter struct {
	Kind, ID, Q, Sort, Following, OwnerID, State string
	OnlyTargets, KnownOnly                       bool
	Page, Limit                                  int
}

type ContentPage struct {
	Items              []ContentItem
	Total, Page, Limit int
}
type DashboardSummary struct{ Videos, Creators, Following, Updates, Issues int64 }
type MetricPoint struct {
	At    int64 `json:"at"`
	Value int64 `json:"value"`
}
type MetricSeries struct {
	Points    []MetricPoint `json:"points"`
	Truncated bool          `json:"truncated"`
}
type VersionEntry struct {
	Kind, ID, Name string
	Seq, At        int64
}
type VersionPage struct {
	Items              []VersionEntry
	Total, Page, Limit int
}
type Snapshot struct {
	Seq, At   int64
	SourceURL string
	Payload   []byte
}

// RecordTargetResult 覆盖最近状态。成功仅在从失败恢复时记录恢复时间。
// 目标已停止关注时 UPDATE 不产生新目标；错误分类不包含原始上游正文。
func (s *Store) RecordTargetResult(ctx context.Context, target Target, result string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tracking_targets SET
last_result = :result,
last_success_at = CASE WHEN :result = 'ok' THEN :at ELSE last_success_at END,
failure_count = CASE WHEN :result = 'ok' THEN 0 ELSE failure_count + 1 END,
last_failure_at = CASE WHEN :result <> 'ok' THEN :at ELSE last_failure_at END,
failure_reason = CASE WHEN :result <> 'ok' THEN :result ELSE failure_reason END,
recovered_at = CASE WHEN :result <> 'ok' THEN NULL WHEN failure_count > 0 THEN :at ELSE recovered_at END
WHERE platform = :platform AND target_type = :kind AND target_id = :id`,
		sql.Named("result", result), sql.Named("at", at.UnixMilli()), sql.Named("platform", target.Platform), sql.Named("kind", target.TargetType), sql.Named("id", target.TargetID))
	return err
}

// catalogCTE 在分页后再读取图片，避免将大快照带入列表排序和计数。
const catalogCTE = `WITH items AS (
SELECT 'video' kind, v.video_id id, v.title name, v.creator_id owner_id, c.name owner_name,
v.view_count metric, v.current_seq seq, 0 video_count, v.first_seen_at first_seen,
v.last_observed_at observed, COALESCE(v.published_at,0) published, 1 recorded
FROM videos v JOIN creators c ON c.platform=v.platform AND c.creator_id=v.creator_id WHERE v.platform=:platform
UNION ALL
SELECT 'creator', c.creator_id, c.name, '', '', c.follower_count, COALESCE(c.current_profile_seq,0),
(SELECT COUNT(*) FROM videos v WHERE v.platform=c.platform AND v.creator_id=c.creator_id),
c.first_seen_at, COALESCE(c.last_profile_observed_at,0), 0, 1 FROM creators c WHERE c.platform=:platform
UNION ALL
SELECT t.target_type,t.target_id,t.target_id,'','',NULL,0,0,t.created_at,0,0,0 FROM tracking_targets t
WHERE t.platform=:platform AND NOT EXISTS (SELECT 1 FROM videos v WHERE t.target_type='video' AND v.platform=t.platform AND v.video_id=t.target_id)
AND NOT EXISTS (SELECT 1 FROM creators c WHERE t.target_type='creator' AND c.platform=t.platform AND c.creator_id=t.target_id)
), windows AS (
SELECT subject_type,subject_id,metric,MIN(sampled_at) first_at,MAX(sampled_at) last_at,COUNT(*) samples
FROM metric_samples WHERE platform=:platform AND sampled_at>=:since AND sampled_at<=:now AND metric IN ('view','follower')
GROUP BY subject_type,subject_id,metric
), enriched AS (
SELECT i.*,COALESCE(t.enabled,0) following,COALESCE(t.interval_seconds,0) interval_seconds,
COALESCE(t.next_run_at,0) next_run,COALESCE(t.last_run_at,0) last_run,COALESCE(t.last_result,'') last_result,
COALESCE(t.last_success_at,NULLIF(i.observed,0),0) last_success,COALESCE(t.failure_count,0) failures,
COALESCE(t.last_failure_at,0) failure_at,COALESCE(t.failure_reason,'') failure_reason,COALESCE(t.recovered_at,0) recovered,
CASE WHEN g.samples>=2 THEN last.value-first.value ELSE NULL END growth,COALESCE(g.first_at,0) growth_from,COALESCE(g.last_at,0) growth_to
FROM items i LEFT JOIN tracking_targets t ON t.platform=:platform AND t.target_type=i.kind AND t.target_id=i.id
LEFT JOIN windows g ON g.subject_type=i.kind AND g.subject_id=i.id AND g.metric=CASE i.kind WHEN 'video' THEN 'view' ELSE 'follower' END
LEFT JOIN metric_samples first ON first.platform=:platform AND first.subject_type=i.kind AND first.subject_id=i.id AND first.metric=g.metric AND first.sampled_at=g.first_at
LEFT JOIN metric_samples last ON last.platform=:platform AND last.subject_type=i.kind AND last.subject_id=i.id AND last.metric=g.metric AND last.sampled_at=g.last_at
) `

const issuePredicate = `(last_result NOT IN ('','ok') OR (last_success>0 AND last_success<:now-(MAX(interval_seconds*2,1800)+1800)*1000))`

func catalogWhere(f CatalogFilter) string {
	where := " WHERE 1=1"
	if !f.OnlyTargets {
		where += " AND recorded=1"
	} else {
		where += " AND following=1"
	}
	if f.Kind != "" {
		where += " AND kind=:kind"
	}
	if f.ID != "" {
		where += " AND id=:id"
	}
	if f.OwnerID != "" {
		where += " AND owner_id=:owner"
	}
	if f.Q != "" {
		where += ` AND (name LIKE :needle ESCAPE '\' OR id LIKE :needle ESCAPE '\' OR owner_name LIKE :needle ESCAPE '\')`
	}
	if f.Following == "yes" {
		where += " AND following=1"
	}
	if f.Following == "no" {
		where += " AND following=0"
	}
	if f.State == "issues" {
		where += " AND following=1 AND " + issuePredicate
	}
	if f.State == "recovered" {
		where += " AND following=1 AND last_result='ok' AND recovered>=:since AND NOT " + issuePredicate
	}
	if f.Sort == "growth" {
		where += " AND growth IS NOT NULL"
	}
	if f.KnownOnly {
		where += " AND metric IS NOT NULL"
	}
	return where
}

func pageBounds(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	if limit < 1 {
		limit = 18
	}
	if limit > 48 {
		limit = 48
	}
	return page, limit
}

// Catalog 包含未成功采集的目标时必须显式设置 OnlyTargets；内容库仅返回已有记录。
func (s *Store) Catalog(ctx context.Context, platform string, f CatalogFilter, now time.Time) (ContentPage, error) {
	if f.Kind != "" && f.Kind != "video" && f.Kind != "creator" {
		return ContentPage{}, errors.New("invalid content kind")
	}
	f.Page, f.Limit = pageBounds(f.Page, f.Limit)
	escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(f.Q))
	args := []any{sql.Named("platform", platform), sql.Named("since", now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli()), sql.Named("now", now.UnixMilli())}
	if f.Kind != "" {
		args = append(args, sql.Named("kind", f.Kind))
	}
	if f.ID != "" {
		args = append(args, sql.Named("id", f.ID))
	}
	if f.OwnerID != "" {
		args = append(args, sql.Named("owner", f.OwnerID))
	}
	if f.Q != "" {
		args = append(args, sql.Named("needle", "%"+escaped+"%"))
	}
	where := catalogWhere(f)
	page := ContentPage{Items: []ContentItem{}, Page: f.Page, Limit: f.Limit}
	if err := s.db.QueryRowContext(ctx, catalogCTE+"SELECT COUNT(*) FROM enriched"+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count catalog: %w", err)
	}
	f.Page = min(f.Page, max(1, (page.Total+f.Limit-1)/f.Limit))
	page.Page = f.Page
	sorts := map[string]string{"popular": "metric DESC NULLS LAST", "growth": "growth DESC NULLS LAST", "title": "name COLLATE NOCASE", "published": "published DESC", "discovered": "first_seen DESC"}
	order := sorts[f.Sort]
	if order == "" {
		order = "observed DESC"
	}
	args = append(args, sql.Named("limit", f.Limit), sql.Named("offset", (f.Page-1)*f.Limit))
	query := catalogCTE + `, selected AS (SELECT * FROM enriched` + where + ` ORDER BY ` + order + `,kind,id LIMIT :limit OFFSET :offset)
SELECT p.*, COALESCE(CASE WHEN p.kind='video' THEN json_extract(v.payload,'$.pic') ELSE json_extract(c.payload,'$.card.face') END,'')
FROM selected p LEFT JOIN video_versions v ON p.kind='video' AND v.platform=:platform AND v.video_id=p.id AND v.seq=p.seq
LEFT JOIN creator_versions c ON p.kind='creator' AND c.platform=:platform AND c.creator_id=p.id AND c.seq=p.seq ORDER BY p.` + order + `,p.kind,p.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, fmt.Errorf("read catalog: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanContent(rows, now)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

func scanContent(rows *sql.Rows, now time.Time) (ContentItem, error) {
	var i ContentItem
	var metric, growth sql.NullInt64
	err := rows.Scan(&i.Kind, &i.ID, &i.Name, &i.OwnerID, &i.OwnerName, &metric, &i.Seq, &i.VideoCount, &i.FirstSeenAt, &i.ObservedAt, &i.PublishedAt, &i.Recorded,
		&i.Following, &i.IntervalSeconds, &i.NextRunAt, &i.LastRunAt, &i.LastResult, &i.LastSuccessAt, &i.Failures, &i.LastFailureAt, &i.FailureReason, &i.RecoveredAt, &growth, &i.GrowthFrom, &i.GrowthTo, &i.Cover)
	if metric.Valid {
		i.Metric = &metric.Int64
	}
	if growth.Valid {
		i.Growth = &growth.Int64
	}
	i.Status = "ok"
	if !i.Following {
		i.Status = "unfollowed"
	} else if i.LastResult != "" && i.LastResult != "ok" {
		i.Status = "error"
	} else if i.LastSuccessAt == 0 {
		i.Status = "pending"
	} else if now.UnixMilli()-i.LastSuccessAt > (max(i.IntervalSeconds*2, 1800)+1800)*1000 {
		i.Status = "stale"
	} else if i.RecoveredAt > 0 {
		i.Status = "recovered"
	}
	if i.Kind == "creator" && i.Seq == 0 && i.Status == "unfollowed" {
		i.Status = "profile_pending"
	}
	return i, err
}

func (s *Store) Content(ctx context.Context, platform, kind, id string, now time.Time) (ContentItem, error) {
	p, err := s.Catalog(ctx, platform, CatalogFilter{Kind: kind, ID: id, Limit: 1}, now)
	if err != nil {
		return ContentItem{}, err
	}
	if len(p.Items) == 0 {
		return ContentItem{}, sql.ErrNoRows
	}
	return p.Items[0], nil
}

func (s *Store) Summary(ctx context.Context, platform string, now time.Time) (DashboardSummary, error) {
	var summary DashboardSummary
	err := s.db.QueryRowContext(ctx, `SELECT
(SELECT COUNT(*) FROM videos WHERE platform=:p),
(SELECT COUNT(*) FROM creators WHERE platform=:p),
(SELECT COUNT(*) FROM tracking_targets WHERE platform=:p AND enabled=1),
(SELECT COUNT(*) FROM video_versions WHERE platform=:p AND seq>1 AND observed_at>=:since)+(SELECT COUNT(*) FROM creator_versions WHERE platform=:p AND seq>1 AND observed_at>=:since)`, sql.Named("p", platform), sql.Named("since", now.AddDate(0, 0, -7).UnixMilli())).Scan(&summary.Videos, &summary.Creators, &summary.Following, &summary.Updates)
	if err != nil {
		return summary, err
	}
	issues, err := s.Catalog(ctx, platform, CatalogFilter{OnlyTargets: true, State: "issues", Limit: 1}, now)
	summary.Issues = int64(issues.Total)
	return summary, err
}

// Metrics 按 UTC 日窗口读取；全量最多返回最近 5000 点，缺失日不补零。
func (s *Store) Metrics(ctx context.Context, platform, kind, id string, days int, now time.Time) (MetricSeries, error) {
	metric := "view"
	if kind == "creator" {
		metric = "follower"
	} else if kind != "video" {
		return MetricSeries{}, errors.New("invalid metric kind")
	}
	if days != 0 && days != 7 && days != 30 && days != 90 {
		return MetricSeries{}, errors.New("invalid metric range")
	}
	from := int64(0)
	if days > 0 {
		from = now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -days+1).UnixMilli()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sampled_at,value FROM metric_samples WHERE platform=? AND subject_type=? AND subject_id=? AND metric=? AND sampled_at>=? AND sampled_at<=? ORDER BY sampled_at DESC LIMIT 5001`, platform, kind, id, metric, from, now.UnixMilli())
	if err != nil {
		return MetricSeries{}, err
	}
	defer rows.Close()
	result := MetricSeries{Points: []MetricPoint{}}
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.At, &p.Value); err != nil {
			return result, err
		}
		result.Points = append(result.Points, p)
	}
	if len(result.Points) > 5000 {
		result.Truncated = true
		result.Points = result.Points[:5000]
	}
	for left, right := 0, len(result.Points)-1; left < right; left, right = left+1, right-1 {
		result.Points[left], result.Points[right] = result.Points[right], result.Points[left]
	}
	return result, rows.Err()
}

// CollectionSeries 是累计已入库视频规模，而非播放量的插值或历史回填。
func (s *Store) CollectionSeries(ctx context.Context, platform string, now time.Time) (MetricSeries, error) {
	start := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -13)
	var earliest sql.NullInt64
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT MIN(first_seen_at),COUNT(*) FROM videos WHERE platform=? AND first_seen_at<?", platform, start.UnixMilli()).Scan(&earliest, &total); err != nil {
		return MetricSeries{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (first_seen_at/86400000)*86400000,COUNT(*) FROM videos WHERE platform=? AND first_seen_at>=? AND first_seen_at<=? GROUP BY first_seen_at/86400000 ORDER BY 1`, platform, start.UnixMilli(), now.UnixMilli())
	if err != nil {
		return MetricSeries{}, err
	}
	defer rows.Close()
	daily := map[int64]int64{}
	var first int64
	for rows.Next() {
		var at, count int64
		if err := rows.Scan(&at, &count); err != nil {
			return MetricSeries{}, err
		}
		daily[at] = count
		if first == 0 {
			first = at
		}
	}
	if err := rows.Err(); err != nil {
		return MetricSeries{}, err
	}
	result := MetricSeries{Points: []MetricPoint{}}
	if total == 0 {
		if first == 0 {
			return result, nil
		}
		start = time.UnixMilli(first)
	}
	for day := start; !day.After(now); day = day.AddDate(0, 0, 1) {
		total += daily[day.UnixMilli()]
		result.Points = append(result.Points, MetricPoint{At: day.UnixMilli(), Value: total})
	}
	return result, nil
}

// Versions 从已有版本表生成内容动态；seq=1 不计为内容更新。
func (s *Store) Versions(ctx context.Context, platform, kind, id, q string, days, page, limit int) (VersionPage, error) {
	page, limit = pageBounds(page, limit)
	result := VersionPage{Items: []VersionEntry{}, Page: page, Limit: limit}
	if kind != "" && kind != "video" && kind != "creator" {
		return result, errors.New("invalid version kind")
	}
	base := `WITH entries AS (SELECT 'video' kind,x.video_id id,v.title name,x.seq,x.observed_at at FROM video_versions x JOIN videos v ON v.platform=x.platform AND v.video_id=x.video_id WHERE x.platform=:platform
UNION ALL SELECT 'creator',x.creator_id,c.name,x.seq,x.observed_at FROM creator_versions x JOIN creators c ON c.platform=x.platform AND c.creator_id=x.creator_id WHERE x.platform=:platform) `
	where := " WHERE 1=1"
	args := []any{sql.Named("platform", platform)}
	if id == "" {
		where += " AND seq>1"
	}
	if kind != "" {
		where += " AND kind=:kind"
		args = append(args, sql.Named("kind", kind))
	}
	if id != "" {
		where += " AND id=:id"
		args = append(args, sql.Named("id", id))
	}
	if q != "" {
		where += " AND (name LIKE :q OR id LIKE :q)"
		args = append(args, sql.Named("q", "%"+q+"%"))
	}
	if days > 0 {
		where += " AND at>=:from"
		args = append(args, sql.Named("from", time.Now().AddDate(0, 0, -days).UnixMilli()))
	}
	if err := s.db.QueryRowContext(ctx, base+"SELECT COUNT(*) FROM entries"+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	page = min(page, max(1, (result.Total+limit-1)/limit))
	result.Page = page
	args = append(args, sql.Named("limit", limit), sql.Named("offset", (page-1)*limit))
	rows, err := s.db.QueryContext(ctx, base+"SELECT kind,id,name,seq,at FROM entries"+where+" ORDER BY at DESC,kind,id,seq DESC LIMIT :limit OFFSET :offset", args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v VersionEntry
		if err := rows.Scan(&v.Kind, &v.ID, &v.Name, &v.Seq, &v.At); err != nil {
			return result, err
		}
		result.Items = append(result.Items, v)
	}
	return result, rows.Err()
}

// ReadSnapshot 只读取所选版本；seq=0 表示最新版本。不存在返回 sql.ErrNoRows。
func (s *Store) ReadSnapshot(ctx context.Context, platform, kind, id string, seq int64) (Snapshot, error) {
	table, idColumn := "video_versions", "video_id"
	if kind == "creator" {
		table, idColumn = "creator_versions", "creator_id"
	} else if kind != "video" {
		return Snapshot{}, errors.New("invalid snapshot kind")
	}
	query := "SELECT seq,observed_at,source_url,payload FROM " + table + " WHERE platform=? AND " + idColumn + "=?"
	args := []any{platform, id}
	if seq > 0 {
		query += " AND seq=?"
		args = append(args, seq)
	}
	query += " ORDER BY seq DESC LIMIT 1"
	var snapshot Snapshot
	var payload string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&snapshot.Seq, &snapshot.At, &snapshot.SourceURL, &payload)
	snapshot.Payload = []byte(payload)
	return snapshot, err
}
