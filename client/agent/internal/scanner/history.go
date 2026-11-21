// history.go 同步本地观看历史并分页、按 UP 聚合；历史只落本地 history.json。
// 同步最多保留 30 天，按游标分页取最近记录，不下载媒体、不写服务器。见 docs/client-catalog.md。
package scanner

import (
	"context"
	"errors"
	"sort"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

const (
	historyVersion    = 1
	historyRetention  = 30 * 24 * time.Hour
	historyMaxPages   = 10
	historyMinLimit   = 12
	historyMaxLimit   = 48
	historyDefaultLim = 20
)

type historyState struct {
	Version int                    `json:"version"`
	Items   []bilibili.HistoryItem `json:"items"`
}

// HistorySyncResult 描述一次同步的增量；Removed 是超过 30 天被清理的条数。
type HistorySyncResult struct {
	Added    int       `json:"added"`
	Updated  int       `json:"updated"`
	Removed  int       `json:"removed"`
	Total    int       `json:"total"`
	SyncedAt time.Time `json:"synced_at"`
}

type HistoryVideoPage struct {
	Items []bilibili.HistoryItem `json:"items"`
	Page  int                    `json:"page"`
	Limit int                    `json:"limit"`
	Total int                    `json:"total"`
}

type HistoryCreator struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Videos       int    `json:"videos"`
	LastViewedAt int64  `json:"last_viewed_at"`
}

type HistoryCreatorPage struct {
	Items []HistoryCreator `json:"items"`
	Page  int              `json:"page"`
	Limit int              `json:"limit"`
	Total int              `json:"total"`
}

func historyPageLimit(limit int) int {
	if limit == 0 {
		return historyDefaultLim
	}
	if limit < historyMinLimit {
		return historyMinLimit
	}
	if limit > historyMaxLimit {
		return historyMaxLimit
	}
	return limit
}

func (s *Service) loadHistoryLocked() historyState {
	state := historyState{Version: historyVersion}
	if err := s.store.LoadHistory(&state); err != nil || state.Version != historyVersion {
		return historyState{Version: historyVersion}
	}
	return state
}

// HistorySync 拉取最近 30 天历史，按 bvid 去重并保留最新观看时间；需要本地 Cookie。
func (s *Service) HistorySync(ctx context.Context) (HistorySyncResult, error) {
	s.mu.Lock()
	cookie, closed := s.cookie, s.closed
	s.mu.Unlock()
	if closed {
		return HistorySyncResult{}, errors.New("本地平台服务已关闭。")
	}
	if cookie == "" {
		return HistorySyncResult{}, &bilibili.Error{Code: "login_required", Message: "请先在设置中配置 B站 Cookie。"}
	}
	_, keys, err := s.api.Login(ctx, cookie)
	if err != nil {
		return HistorySyncResult{}, err
	}
	s.mu.Lock()
	state := s.loadHistoryLocked()
	items := make(map[string]bilibili.HistoryItem, len(state.Items))
	for _, item := range state.Items {
		items[item.BVID] = item
	}
	s.mu.Unlock()
	result := HistorySyncResult{}
	var max, viewAt int64
	for page := 0; page < historyMaxPages; page++ {
		loaded, err := s.api.History(ctx, cookie, max, viewAt, keys)
		if err != nil {
			return result, err
		}
		for _, item := range loaded.Items {
			if old, exists := items[item.BVID]; exists {
				if item.ViewedAt > old.ViewedAt {
					items[item.BVID] = item
					result.Updated++
				}
				continue
			}
			items[item.BVID] = item
			result.Added++
		}
		max, viewAt = loaded.Max, loaded.ViewAt
		if !loaded.More {
			break
		}
	}
	cutoff := time.Now().Add(-historyRetention).Unix()
	kept := make([]bilibili.HistoryItem, 0, len(items))
	for _, item := range items {
		if item.ViewedAt < cutoff {
			result.Removed++
			continue
		}
		kept = append(kept, item)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, errors.New("本地平台服务已关闭。")
	}
	state = historyState{Version: historyVersion, Items: kept}
	if err := s.store.SaveHistory(state); err != nil {
		s.mu.Unlock()
		return result, err
	}
	s.mu.Unlock()
	result.Total = len(kept)
	result.SyncedAt = time.Now().UTC()
	return result, nil
}

func historyCutoff(days int) int64 {
	if days < 1 {
		days = 30
	}
	return time.Now().AddDate(0, 0, -days).Unix()
}

// HistoryVideos 返回本地历史视频页，按最近观看时间倒序；days 只过滤本地。
func (s *Service) HistoryVideos(days, page, limit int) HistoryVideoPage {
	return s.HistoryVideosFiltered(days, page, limit, nil)
}

// HistoryVideosFiltered 在统计总量和分页前按 BVID 筛选；include=nil 表示全部。
// include 在扫描锁外调用，应只查询本地状态，不执行网络或写入操作。
func (s *Service) HistoryVideosFiltered(days, page, limit int, include func(string) bool) HistoryVideoPage {
	limit = historyPageLimit(limit)
	if page < 1 {
		page = 1
	}
	s.mu.Lock()
	state := s.loadHistoryLocked()
	s.mu.Unlock()
	cutoff := historyCutoff(days)
	items := make([]bilibili.HistoryItem, 0, len(state.Items))
	for _, item := range state.Items {
		if item.ViewedAt >= cutoff && (include == nil || include(item.BVID)) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ViewedAt != items[j].ViewedAt {
			return items[i].ViewedAt > items[j].ViewedAt
		}
		return items[i].BVID < items[j].BVID
	})
	total := len(items)
	maxPage := max(1, (total+limit-1)/limit)
	if page > maxPage {
		page = maxPage
	}
	start := (page - 1) * limit
	end := min(start+limit, total)
	return HistoryVideoPage{Items: items[start:end], Page: page, Limit: limit, Total: total}
}

// HistoryCreators 按 author_id 聚合历史视频，按最近观看时间倒序。
func (s *Service) HistoryCreators(days, page, limit int) HistoryCreatorPage {
	return s.HistoryCreatorsFiltered(days, page, limit, nil)
}

// HistoryCreatorsFiltered 按 UP ID 筛选聚合结果后再统计总量和分页，保留观看视频数。
// include=nil 表示全部；回调在扫描锁外执行，只能读取本地状态。
func (s *Service) HistoryCreatorsFiltered(days, page, limit int, include func(string) bool) HistoryCreatorPage {
	limit = historyPageLimit(limit)
	if page < 1 {
		page = 1
	}
	s.mu.Lock()
	state := s.loadHistoryLocked()
	s.mu.Unlock()
	cutoff := historyCutoff(days)
	creators := map[string]*HistoryCreator{}
	for _, item := range state.Items {
		if item.ViewedAt < cutoff {
			continue
		}
		c, ok := creators[item.AuthorID]
		if !ok {
			c = &HistoryCreator{ID: item.AuthorID, Name: item.AuthorName}
			creators[item.AuthorID] = c
		}
		c.Videos++
		if item.ViewedAt > c.LastViewedAt {
			c.LastViewedAt = item.ViewedAt
			if item.AuthorName != "" {
				c.Name = item.AuthorName
			}
		}
	}
	items := make([]HistoryCreator, 0, len(creators))
	for _, c := range creators {
		if include == nil || include(c.ID) {
			items = append(items, *c)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastViewedAt != items[j].LastViewedAt {
			return items[i].LastViewedAt > items[j].LastViewedAt
		}
		return items[i].ID < items[j].ID
	})
	total := len(items)
	maxPage := max(1, (total+limit-1)/limit)
	if page > maxPage {
		page = maxPage
	}
	start := (page - 1) * limit
	end := min(start+limit, total)
	return HistoryCreatorPage{Items: items[start:end], Page: page, Limit: limit, Total: total}
}
