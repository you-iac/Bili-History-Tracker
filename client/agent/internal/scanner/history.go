// history.go 同步本地观看历史并分页、按 UP 聚合；历史只落本地 history.json。
// 同步最多保留 30 天，按游标分页取最近记录，不下载媒体、不写服务器。见 docs/client-catalog.md。
package scanner

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

const (
	historyVersion              = 1
	historyRetention            = 30 * 24 * time.Hour
	historyMaxPages             = 50
	historyIncrementalThreshold = 2
	historyMinLimit             = 12
	historyMaxLimit             = 48
	historyDefaultLim           = 20
)

type historyState struct {
	Version    int                    `json:"version"`
	Items      []bilibili.HistoryItem `json:"items"`
	LastSyncAt time.Time              `json:"last_sync_at"`
}

// HistorySyncResult 描述一次同步的增量；Removed 是超过 30 天被清理的条数。
type HistorySyncResult struct {
	Added    int       `json:"added"`
	Updated  int       `json:"updated"`
	Removed  int       `json:"removed"`
	Total    int       `json:"total"`
	Pages    int       `json:"pages"`
	SyncedAt time.Time `json:"synced_at"`
}

// HistoryVideo 是按 bvid 聚合后的展示项：主字段取最近一次观看，Views 是观看次数。
type HistoryVideo struct {
	bilibili.HistoryItem
	Views         int   `json:"views"`
	FirstViewedAt int64 `json:"first_viewed_at"`
}

type HistoryVideoPage struct {
	Items []HistoryVideo `json:"items"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
	Total int            `json:"total"`
}

func historyKey(bvid string, viewedAt int64) string {
	return bvid + "@" + strconv.FormatInt(viewedAt, 10)
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

// HistorySync 增量拉取观看历史：首次同步翻到接口末尾，之后遇到新增很少的一页就停止。
// 按 bvid 去重并保留最新观看时间；需要本地 Cookie。
func (s *Service) HistorySync(ctx context.Context) (HistorySyncResult, error) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
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
		items[historyKey(item.BVID, item.ViewedAt)] = item
	}
	s.mu.Unlock()
	existing := len(items)
	result := HistorySyncResult{}
	var max, viewAt int64
	for page := 0; page < historyMaxPages; page++ {
		loaded, err := s.api.History(ctx, cookie, max, viewAt, keys)
		if err != nil {
			return result, err
		}
		result.Pages++
		added := 0
		for _, item := range loaded.Items {
			key := historyKey(item.BVID, item.ViewedAt)
			if old, exists := items[key]; exists {
				// 历史接口不返回互动状态，合并时保留本地已查询到的结果。
				item.Like, item.Coin, item.Favorite = old.Like, old.Coin, old.Favorite
				item.InteractionCheckedAt, item.InteractionFailures, item.InteractionNextAttempt = old.InteractionCheckedAt, old.InteractionFailures, old.InteractionNextAttempt
				items[key] = item
				result.Updated++
				continue
			}
			// 同一视频的旧观看记录已查过互动时，新记录直接继承，避免重复查询。
			for _, old := range items {
				if old.BVID == item.BVID {
					item.Like, item.Coin, item.Favorite = old.Like, old.Coin, old.Favorite
					item.InteractionCheckedAt, item.InteractionFailures, item.InteractionNextAttempt = old.InteractionCheckedAt, old.InteractionFailures, old.InteractionNextAttempt
					break
				}
			}
			items[key] = item
			result.Added++
			added++
		}
		// 已有历史时，最新几页都没什么新增，说明后面只会更旧，停止继续翻页。
		if existing > 0 && added <= historyIncrementalThreshold {
			break
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
	state = historyState{Version: historyVersion, Items: kept, LastSyncAt: time.Now().UTC()}
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
	grouped := map[string][]bilibili.HistoryItem{}
	for _, item := range state.Items {
		if item.ViewedAt >= cutoff && (include == nil || include(item.BVID)) {
			grouped[item.BVID] = append(grouped[item.BVID], item)
		}
	}
	items := make([]HistoryVideo, 0, len(grouped))
	for _, group := range grouped {
		latest, first := group[0], group[0].ViewedAt
		for _, item := range group {
			if item.ViewedAt > latest.ViewedAt {
				latest = item
			}
			if item.ViewedAt < first {
				first = item.ViewedAt
			}
		}
		items = append(items, HistoryVideo{HistoryItem: latest, Views: len(group), FirstViewedAt: first})
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
	seen := map[string]map[string]bool{}
	for _, item := range state.Items {
		if item.ViewedAt < cutoff {
			continue
		}
		c, ok := creators[item.AuthorID]
		if !ok {
			c = &HistoryCreator{ID: item.AuthorID, Name: item.AuthorName}
			creators[item.AuthorID] = c
			seen[item.AuthorID] = map[string]bool{}
		}
		if !seen[item.AuthorID][item.BVID] {
			c.Videos++
			seen[item.AuthorID][item.BVID] = true
		}
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

const historyInteractionBudget = 60

// InteractionResult 描述一轮互动查询；FavoriteFailed 表示收藏集合本次不可用。
type InteractionResult struct {
	Checked        int  `json:"checked"`
	Liked          int  `json:"liked"`
	Coined         int  `json:"coined"`
	Favorited      int  `json:"favorited"`
	Failed         int  `json:"failed"`
	FavoriteFailed bool `json:"favorite_failed"`
}

// CheckInteractions 按 interaction_checked_at 最旧优先，补查最多 limit 条历史视频的互动状态。
// 收藏集合每轮只拉一次；单条失败不影响其他条目。
func (s *Service) CheckInteractions(ctx context.Context, limit int) (InteractionResult, error) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if limit <= 0 || limit > historyInteractionBudget {
		limit = historyInteractionBudget
	}
	s.mu.Lock()
	cookie, closed := s.cookie, s.closed
	state := s.loadHistoryLocked()
	s.mu.Unlock()
	if closed {
		return InteractionResult{}, errors.New("本地平台服务已关闭。")
	}
	if cookie == "" {
		return InteractionResult{}, &bilibili.Error{Code: "login_required", Message: "请先在设置中配置 B站 Cookie。"}
	}
	_, keys, err := s.api.Login(ctx, cookie)
	if err != nil {
		return InteractionResult{}, err
	}
	result := InteractionResult{}
	groups := map[string][]int{}
	for index := range state.Items {
		item := &state.Items[index]
		groups[item.BVID] = append(groups[item.BVID], index)
	}
	candidates := make([]struct {
		bvid      string
		indexes   []int
		checkedAt int64
	}, 0, len(groups))
	for bvid, indexes := range groups {
		latest := indexes[0]
		for _, index := range indexes {
			if state.Items[index].ViewedAt > state.Items[latest].ViewedAt {
				latest = index
			}
		}
		candidates = append(candidates, struct {
			bvid      string
			indexes   []int
			checkedAt int64
		}{bvid: bvid, indexes: indexes, checkedAt: state.Items[latest].InteractionCheckedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].checkedAt < candidates[j].checkedAt
	})
	now := time.Now().Unix()
	attempted := 0
	for _, candidate := range candidates {
		if attempted >= limit {
			break
		}
		if ctx.Err() != nil {
			break
		}
		latest := candidate.indexes[0]
		for _, index := range candidate.indexes {
			if state.Items[index].ViewedAt > state.Items[latest].ViewedAt {
				latest = index
			}
		}
		if state.Items[latest].InteractionNextAttempt > now {
			continue
		}
		// 最近 3 天每 6 小时重查，3-30 天每 24 小时重查；首次查询不受限。
		if checked := state.Items[latest].InteractionCheckedAt; checked > 0 {
			ttl := int64(24 * 3600)
			if now-state.Items[latest].ViewedAt <= 3*24*3600 {
				ttl = 6 * 3600
			}
			if now-checked < ttl {
				continue
			}
		}
		attempted++
		relation, err := s.api.ArchiveRelation(ctx, cookie, candidate.bvid, keys)
		if err != nil {
			// 取消或超时不是平台失败，不累计未完成查询的退避。
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Failed++
			for _, index := range candidate.indexes {
				state.Items[index].InteractionFailures++
				state.Items[index].InteractionNextAttempt = interactionBackoff(state.Items[index].InteractionFailures)
			}
			continue
		}
		for _, index := range candidate.indexes {
			item := &state.Items[index]
			item.Like, item.Coin, item.Favorite = relation.Like, relation.Coin, relation.Favorite
			item.InteractionCheckedAt = now
			item.InteractionFailures = 0
			item.InteractionNextAttempt = 0
		}
		result.Checked++
		if relation.Like {
			result.Liked++
		}
		if relation.Coin > 0 {
			result.Coined++
		}
		if relation.Favorite {
			result.Favorited++
		}
	}
	if result.Checked == 0 && result.Failed == 0 {
		return result, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, errors.New("本地平台服务已关闭。")
	}
	state.Version = historyVersion
	if err := s.store.SaveHistory(state); err != nil {
		s.mu.Unlock()
		return result, err
	}
	s.mu.Unlock()
	return result, nil
}

// interactionBackoff 返回失败后的最早重试时间：5 分钟起，指数退避，最多 6 小时。
func interactionBackoff(failures int) int64 {
	if failures < 1 {
		failures = 1
	}
	delay := 5 * time.Minute
	for i := 1; i < failures && delay < 6*time.Hour; i++ {
		delay *= 2
	}
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	return time.Now().Add(delay).Unix()
}
