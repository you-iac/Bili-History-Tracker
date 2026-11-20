// catalog_identity.go 为公共资料池提供轻量目标目录；当前资料由Agent本地补全。
// 普通目录不读版本/任务/投稿汇总；热门排序仅计算对应类型的历史增量。见 docs/local-first-metadata.md。
package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CatalogIdentity 返回分页身份索引；普通排序不读取历史窗口，热门排序只计算本类型增量。
func (s *Store) CatalogIdentity(ctx context.Context, platform string, f CatalogFilter, now time.Time) (ContentPage, error) {
	table, id, seq, observed, name, metric, published := "videos", "video_id", "current_seq", "last_observed_at", "title", "view_count", "COALESCE(i.published_at,0)"
	if f.Kind == "creator" {
		table, id, seq, observed, name, metric, published = "creators", "creator_id", "COALESCE(current_profile_seq,0)", "COALESCE(last_profile_observed_at,0)", "name", "follower_count", "observed"
	} else if f.Kind != "video" {
		return ContentPage{}, fmt.Errorf("invalid catalog kind")
	}
	f.Page, f.Limit = pageBounds(f.Page, f.Limit)
	where, args := " WHERE i.platform=?", []any{platform}
	if f.ID != "" {
		where += " AND i." + id + "=?"
		args = append(args, f.ID)
	}
	if f.OwnerID != "" {
		if f.Kind == "video" {
			where += " AND i.creator_id=?"
			args = append(args, f.OwnerID)
		} else {
			where += " AND 0"
		}
	}
	if f.Q != "" {
		escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(f.Q))
		needle := "%" + escaped + "%"
		where += " AND (i." + id + " LIKE ? ESCAPE char(92) OR i." + name + " LIKE ? ESCAPE char(92)"
		args = append(args, needle, needle)
		if f.Kind == "video" {
			where += " OR EXISTS (SELECT 1 FROM creators c WHERE c.platform=i.platform AND c.creator_id=i.creator_id AND c.name LIKE ? ESCAPE char(92))"
			args = append(args, needle)
		}
		where += ")"
	}
	page := ContentPage{Items: []ContentItem{}, Page: f.Page, Limit: f.Limit}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" i"+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	page.Page = min(page.Page, max(1, (page.Total+page.Limit-1)/page.Limit))
	order := "observed DESC"
	prefix, join := "", ""
	readArgs := append([]any{}, args...)
	switch f.Sort {
	case "popular", "views":
		order = "i." + metric + " DESC NULLS LAST"
	case "title":
		order = "i." + name + " COLLATE NOCASE"
	case "published":
		order = published + " DESC"
	case "discovered":
		order = "i.first_seen_at DESC"
	case "hot", "growth":
		label := "view"
		if f.Kind == "creator" {
			label = "follower"
		}
		prefix = `WITH windows AS (SELECT subject_id,MIN(sampled_at) first_at,MAX(sampled_at) last_at,COUNT(*) samples FROM metric_samples
WHERE platform=? AND subject_type=? AND metric=? AND sampled_at>=? AND sampled_at<=? GROUP BY subject_id),
changes AS (SELECT g.subject_id,CASE WHEN g.samples>=2 THEN last.value-first.value END growth FROM windows g
JOIN metric_samples first ON first.platform=? AND first.subject_type=? AND first.metric=? AND first.subject_id=g.subject_id AND first.sampled_at=g.first_at
JOIN metric_samples last ON last.platform=first.platform AND last.subject_type=first.subject_type AND last.subject_id=first.subject_id AND last.metric=first.metric AND last.sampled_at=g.last_at) `
		join = " LEFT JOIN changes g ON g.subject_id=i." + id
		readArgs = append([]any{platform, f.Kind, label, now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli(), now.UnixMilli(), platform, f.Kind, label}, args...)
		order = "g.growth DESC NULLS LAST"
	}
	query := prefix + "SELECT i." + id + "," + seq + "," + observed + " observed FROM " + table + " i" + join + where + " ORDER BY " + order + ",i." + id + " LIMIT ? OFFSET ?"
	readArgs = append(readArgs, page.Limit, (page.Page-1)*page.Limit)
	rows, err := s.db.QueryContext(ctx, query, readArgs...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item := ContentItem{Kind: f.Kind, Recorded: true}
		if err := rows.Scan(&item.ID, &item.Seq, &item.ObservedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}
