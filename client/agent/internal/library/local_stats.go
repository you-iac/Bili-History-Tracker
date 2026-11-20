// local_stats.go 只用本地已关注视频计算UP卡片汇总，不查询服务器目录或枚举平台。
// 数量缺失时保留未知；归属使用已知主作者，缺失时使用明确扫描关联。见 docs/local-first-metadata.md。
package library

import (
	"math"
	"time"

	"bili-history-tracker/client/agent/internal/remote"
)

type creatorStats struct {
	count, views, recent, published int64
	unknownViews, unknownPublished  bool
}

func localCreatorStats(follows []Follow, now time.Time) map[string]creatorStats {
	result := map[string]creatorStats{}
	since := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli()
	for _, f := range follows {
		if f.Kind != "video" {
			continue
		}
		i := followItem(f)
		owner := i.OwnerID
		if owner == "" {
			owner = f.DiscoveryOwnerID
		}
		if owner == "" {
			continue
		}
		s := result[owner]
		s.count++
		if i.Metric == nil || *i.Metric < 0 || *i.Metric > math.MaxInt64-s.views {
			s.unknownViews = true
		} else {
			s.views += *i.Metric
		}
		if i.PublishedAt == 0 {
			s.unknownPublished = true
		} else {
			if i.PublishedAt >= since && i.PublishedAt <= now.UnixMilli() {
				s.recent++
			}
			s.published = max(s.published, i.PublishedAt)
		}
		result[owner] = s
	}
	return result
}

func applyLocalStats(item remote.Item, stats map[string]creatorStats) remote.Item {
	s := stats[item.ID]
	item.VideoViews, item.RecentVideos = nil, nil
	item.VideoCount = &s.count
	if !s.unknownViews {
		value := s.views
		item.VideoViews = &value
	}
	if !s.unknownPublished {
		value := s.recent
		item.RecentVideos = &value
	}
	item.PublishedAt = s.published
	return item
}
