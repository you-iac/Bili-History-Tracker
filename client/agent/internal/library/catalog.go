// catalog.go 在分页前统一筛选、排序本地关注和投稿发现结果；浏览列表不请求平台。
// 未知指标排末尾，归属取资料/扫描上下文，不把服务器跟踪当作个人关注。见 docs/client-catalog.md。
package library

import (
	"sort"
	"strings"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

// ValidSort 是本地与远程目录共享的排序白名单。
func ValidSort(order string) bool {
	switch order {
	case "", "added", "popular", "views", "hot", "published", "observed", "title":
		return true
	}
	return false
}

// SearchQuery 将平台链接变成精确标识，普通文本保留名称搜索语义。
// 无效 URL 返回输入错误，绝不访问输入网址。
func SearchQuery(kind, q string) (string, error) {
	q = strings.TrimSpace(q)
	if id, err := Normalize(kind, q); err == nil {
		return id, nil
	}
	if strings.Contains(q, "://") || strings.HasPrefix(q, "www.") || strings.HasPrefix(q, "space.bilibili.") {
		return "", &Error{Message: "请输入有效的 B站空间/视频链接，或名称、UID、BV 号。", Status: 400}
	}
	return q, nil
}

func followItem(f Follow) remote.Item {
	if f.Current != nil && f.Current.Available {
		return f.Current.Item
	}
	if f.Summary != nil {
		return *f.Summary
	}
	if f.Data != nil {
		return f.Data.Item
	}
	if v := f.Discovery; v != nil {
		return remote.Item{Kind: "video", ID: f.ID, Name: v.Title, OwnerID: f.DiscoveryOwnerID, PublishedAt: v.PublishedAt, Metric: v.Views, Cover: v.Cover}
	}
	return remote.Item{Kind: f.Kind, ID: f.ID}
}

func descending(a, b *int64) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if *a > *b {
		return -1
	}
	if *a < *b {
		return 1
	}
	return 0
}

func positiveValue(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}

func publicationValue(n int64) *int64 {
	if n > 0 && n < 1e12 {
		n *= 1000
	}
	return positiveValue(n)
}

// compareItems 只比较排序键；调用者用 ID 作为稳定的次序键。
func compareItems(a, b remote.Item, order string) int {
	switch order {
	case "popular":
		return descending(a.Metric, b.Metric)
	case "views":
		if a.Kind == "video" {
			return descending(a.Metric, b.Metric)
		}
		return descending(a.VideoViews, b.VideoViews)
	case "hot":
		return descending(a.Growth, b.Growth)
	case "published":
		return descending(publicationValue(a.PublishedAt), publicationValue(b.PublishedAt))
	case "observed":
		return descending(positiveValue(a.ObservedAt), positiveValue(b.ObservedAt))
	case "title":
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	return 0
}

// ListCatalog 对全部本地关注筛选和排序后分页；owner 只匹配已知所属 UP 的视频。
func (s *Service) ListCatalog(kind, q string, page int, pendingOnly bool, order, owner string, scanned ...[]bilibili.Video) Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Page{Items: []Follow{}, Limit: 20}
	exactID, _ := Normalize(kind, q)
	if normalized, err := SearchQuery(kind, q); err == nil {
		q = normalized
	}
	q = strings.ToLower(strings.TrimSpace(q))
	items := []Follow{}
	known := map[string]bilibili.Video{}
	if owner != "" && len(scanned) > 0 {
		for _, v := range scanned[0] {
			known[v.ID] = v
		}
	}
	for _, f := range s.state.Follows {
		if current, ok := s.state.Current[targetKey(f.Kind, f.ID)]; ok && current.Item.ObservedAt >= followItem(f).ObservedAt && (current.Source == "platform" || current.ServerURL == s.state.ServerURL) {
			current.LocalFollow = nil
			f.Current = &current
		}
		if f.Kind == "creator" {
			result.Creators++
		} else {
			result.Videos++
		}
		i := followItem(f)
		if v, ok := known[f.ID]; ok && f.Kind == "video" {
			if f.Data == nil && f.Current == nil {
				i = remote.Item{Kind: "video", ID: f.ID, Name: v.Title, PublishedAt: v.PublishedAt, Metric: v.Views, Cover: v.Cover}
			}
			i.OwnerID = owner
			copy := remote.Detail{Item: i}
			f.Data = &copy
		}
		matches := q == "" || strings.Contains(strings.ToLower(f.ID+" "+i.Name+" "+i.OwnerName), q)
		if exactID != "" {
			matches = f.ID == exactID
		}
		if f.Data == nil && f.Discovery != nil {
			f.Data = &remote.Detail{Item: i}
		}
		if (kind == "" || f.Kind == kind) && (!pendingOnly || f.Pending) && (owner == "" || f.Kind == "video" && (i.OwnerID == owner || f.DiscoveryOwnerID == owner)) && matches {
			items = append(items, f)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if order == "" || order == "added" {
			if !items[i].AddedAt.Equal(items[j].AddedAt) {
				return items[i].AddedAt.After(items[j].AddedAt)
			}
		} else if cmp := compareItems(followItem(items[i]), followItem(items[j]), order); cmp != 0 {
			return cmp < 0
		}
		return items[i].ID < items[j].ID
	})
	result.Total = len(items)
	result.Page = min(max(1, page), max(1, (len(items)+19)/20))
	start := (result.Page - 1) * 20
	result.Items = append(result.Items, items[start:min(start+20, len(items))]...)
	return result
}

// RememberDiscovery 在用户明确关注后保存投稿上下文；扫描任务替换或远程失败不会丢失 UP 关联。
// 独立保存扫描资料，不把平台枚举结果冒充服务器成功观测或改写视频主作者。
func (s *Service) RememberDiscovery(id, owner string, video bilibili.Video) (Follow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	for index, f := range next.Follows {
		if f.Kind == "video" && f.ID == id {
			next.Follows[index].DiscoveryOwnerID = owner
			next.Follows[index].Discovery = &video
			return next.Follows[index], s.save(next)
		}
	}
	return Follow{}, &Error{Message: "视频尚未本地关注。", Status: 404}
}

// ScanCatalog 把已枚举投稿转换为共用卡片资料，过滤已关注项后再搜索、排序和分页。
// 扫描 UP 上下文包含合作投稿，不能用视频主作者替换此归属。
func (s *Service) ScanCatalog(videos []bilibili.Video, owner, q, order string, page int) PublicPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	followed := map[string]bool{}
	exactID, _ := Normalize("video", q)
	for _, f := range s.state.Follows {
		if f.Kind == "video" {
			followed[f.ID] = true
		}
	}
	if normalized, err := SearchQuery("video", q); err == nil {
		q = normalized
	}
	q = strings.ToLower(strings.TrimSpace(q))
	items := []PublicItem{}
	for _, v := range videos {
		matches := q == "" || strings.Contains(strings.ToLower(v.ID+" "+v.Title), q)
		if exactID != "" {
			matches = v.ID == exactID
		}
		if followed[v.ID] || !matches {
			continue
		}
		items = append(items, PublicItem{Item: remote.Item{Kind: "video", ID: v.ID, Name: v.Title, OwnerID: owner, PublishedAt: v.PublishedAt, Metric: v.Views, Cover: v.Cover}})
	}
	sort.Slice(items, func(i, j int) bool {
		if cmp := compareItems(items[i].Item, items[j].Item, order); cmp != 0 {
			return cmp < 0
		}
		return items[i].ID < items[j].ID
	})
	result := PublicPage{Items: []PublicItem{}, Page: min(max(1, page), max(1, (len(items)+19)/20)), Limit: 20, Total: len(items)}
	start := (result.Page - 1) * 20
	result.Items = append(result.Items, items[start:min(start+20, len(items))]...)
	return result
}
