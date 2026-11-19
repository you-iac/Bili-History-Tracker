// content_benchmark_test.go 用同一临时库比较详情/目录成本，记录执行计划；不推断真实服务器容量。
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

const perfCreators, perfVideos = 128, 1024

// performanceStore 在一个事务内准备固定规模资料、当前快照、任务和 7 日样本。
func performanceStore(tb testing.TB) (*Store, time.Time) {
	tb.Helper()
	s, err := Open(filepath.Join(tb.TempDir(), "performance.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	tx, err := s.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		tb.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			tb.Fatal(err)
		}
	}
	exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO creators(platform,creator_id,name,follower_count,first_seen_at,last_referenced_at,last_profile_observed_at,current_profile_hash,current_profile_seq)
SELECT 'bilibili',CAST(x AS TEXT),printf('Creator %04d',x),x*1000,?, ?, ?,zeroblob(8),1 FROM n`,
		perfCreators, now.AddDate(0, 0, -10).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	exec(`INSERT INTO creator_versions(platform,creator_id,seq,observed_at,source_url,content_hash,hash_version,payload)
SELECT platform,creator_id,1,last_profile_observed_at,'perf://creator',current_profile_hash,1,
json_object('card',json_object('mid',creator_id,'name',name,'face','https://i0.hdslb.com/face.jpg')) FROM creators`)
	exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<:count)
INSERT INTO videos(platform,video_id,creator_id,title,published_at,duration_seconds,view_count,first_seen_at,last_observed_at,current_hash,current_seq)
SELECT 'bilibili',printf('BVperf%06d',x),CAST(1+(x-1)%:creators AS TEXT),printf('Video %04d',x),:now-x*3600000,60,x*100,:first,:now,zeroblob(8),1 FROM n`,
		sql.Named("count", perfVideos), sql.Named("creators", perfCreators), sql.Named("now", now.UnixMilli()), sql.Named("first", now.AddDate(0, 0, -50).UnixMilli()))
	exec(`INSERT INTO video_versions(platform,video_id,seq,observed_at,source_url,content_hash,hash_version,payload)
SELECT platform,video_id,1,last_observed_at,'perf://video',current_hash,1,
json_object('bvid',video_id,'title',title,'pic','https://i0.hdslb.com/video.jpg') FROM videos`)
	for _, subject := range []struct{ table, kind, id, metric string }{
		{"videos", "video", "video_id", "view"}, {"creators", "creator", "creator_id", "follower"},
	} {
		exec(`WITH RECURSIVE days(d) AS (VALUES(0) UNION ALL SELECT d+1 FROM days WHERE d<6)
INSERT INTO metric_samples(platform,subject_type,subject_id,metric,sampled_at,value)
SELECT platform,'`+subject.kind+`',`+subject.id+`,'`+subject.metric+`',?-d*86400000,100000-d*100 FROM `+subject.table+`,days`, now.Truncate(24*time.Hour).UnixMilli())
		exec(`INSERT INTO tracking_targets(platform,target_type,target_id,interval_seconds,next_run_at,enabled,created_at,last_result,last_success_at)
SELECT platform,'`+subject.kind+`',`+subject.id+`,3600,:now+3600000,1,:now,'ok',:now FROM `+subject.table, sql.Named("now", now.UnixMilli()))
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	if _, err := s.db.Exec("ANALYZE"); err != nil {
		tb.Fatal(err)
	}
	return s, now
}

func BenchmarkContentLookup(b *testing.B) {
	s, now := performanceStore(b)
	for _, subject := range []struct{ kind, id string }{{"video", "BVperf000001"}, {"creator", "1"}} {
		for _, legacy := range []bool{true, false} {
			name := "direct"
			if legacy {
				name = "catalog"
			}
			b.Run(subject.kind+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if legacy {
						page, err := s.Catalog(context.Background(), "bilibili", CatalogFilter{Kind: subject.kind, ID: subject.id, Limit: 1}, now)
						if err != nil || len(page.Items) != 1 {
							b.Fatal(page, err)
						}
					} else if _, err := s.Content(context.Background(), "bilibili", subject.kind, subject.id, now); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkClientPageHistory 对比旧18次目录摘要和新单次批量采样；不含本地平台HTTP耗时。
func BenchmarkClientPageHistory(b *testing.B) {
	s, now := performanceStore(b)
	ids := []string{}
	for n := 1; n <= 18; n++ {
		ids = append(ids, fmt.Sprintf("BVperf%06d", n))
	}
	for _, batch := range []bool{false, true} {
		name := "catalog-per-id"
		if batch {
			name = "metrics-batch"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if batch {
					if _, err := s.MetricsBatch(context.Background(), "bilibili", "video", ids, 7, now); err != nil {
						b.Fatal(err)
					}
				} else {
					for _, id := range ids {
						if _, err := s.Catalog(context.Background(), "bilibili", CatalogFilter{Kind: "video", ID: id, Limit: 1}, now); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func catalogCount(s *Store, kind, order string, now time.Time) error {
	var total int
	return s.db.QueryRow(catalogCTE+"SELECT COUNT(*) FROM enriched"+catalogWhere(CatalogFilter{Kind: kind, Sort: order}),
		sql.Named("platform", "bilibili"), sql.Named("kind", kind),
		sql.Named("since", now.Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli()), sql.Named("now", now.UnixMilli())).Scan(&total)
}

func BenchmarkCatalogList(b *testing.B) {
	s, now := performanceStore(b)
	for _, kind := range []string{"video", "creator"} {
		for _, order := range []string{"observed", "hot"} {
			b.Run(kind+"/"+order+"/page", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Catalog(context.Background(), "bilibili", CatalogFilter{Kind: kind, Sort: order, Limit: 18}, now); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(kind+"/"+order+"/count", func(b *testing.B) {
				for b.Loop() {
					if err := catalogCount(s, kind, order, now); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestReadQueryPlans(t *testing.T) {
	s, now := performanceStore(t)
	type planQuery struct {
		name, query string
		args        []any
	}
	for _, kind := range []string{"video", "creator"} {
		args := []any{sql.Named("platform", "bilibili"), sql.Named("kind", kind),
			sql.Named("since", now.Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli()), sql.Named("now", now.UnixMilli())}
		direct, err := contentQuery(kind)
		if err != nil {
			t.Fatal(err)
		}
		id := "1"
		if kind == "video" {
			id = "BVperf000001"
		}
		queries := []planQuery{{kind + "/direct", direct, append(append([]any{}, args...), sql.Named("id", id))}}
		where := catalogWhere(CatalogFilter{Kind: kind})
		queries = append(queries, planQuery{kind + "/count", catalogCTE + "SELECT COUNT(*) FROM enriched" + where, args})
		for _, sort := range []string{"observed DESC", "growth DESC NULLS LAST"} {
			queries = append(queries, planQuery{kind + "/" + sort, catalogCTE + "SELECT * FROM enriched" + where + " ORDER BY " + sort + ",kind,id LIMIT 18 OFFSET 0", args})
		}
		for _, query := range queries {
			t.Run(query.name, func(t *testing.T) {
				rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query.query, query.args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					t.Logf("%d/%d %s", id, parent, detail)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
