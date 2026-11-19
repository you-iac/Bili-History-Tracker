// current.go 组合本地平台当前资料和服务器批量历史；服务器资料只作失败兜底。
// 当前数量不替代历史采样，缓存与跟踪任务独立。见 docs/local-first-metadata.md。
package library

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

// CurrentSource 只查询明确目标的当前资料，凭据由实现方持有且不对库暴露。
type CurrentSource interface {
	Current(context.Context, string, string) (bilibili.Metadata, error)
}
type currentRecord struct {
	item  remote.Item
	until time.Time
}
type currentCall struct {
	done chan struct{}
	item remote.Item
	err  error
}

func (s *Service) readCurrent(ctx context.Context, kind, id string) (remote.Item, error) {
	key := kind + ":" + id
	s.mu.Lock()
	if cached, ok := s.currentCache[key]; ok && cached.item.MetadataSource == "bilibili" && time.Now().Before(cached.until) {
		s.mu.Unlock()
		return cached.item, nil
	}
	// 关注摘要已落盘，重启后仍可在短TTL内直接使用当前公开资料。
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id && f.Summary != nil && f.Summary.MetadataSource == "bilibili" && f.Summary.MetadataAt > 0 && time.Since(time.UnixMilli(f.Summary.MetadataAt)) < time.Minute {
			i := *f.Summary
			s.mu.Unlock()
			return i, nil
		}
	}
	if call := s.currentCalls[key]; call != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return remote.Item{}, ctx.Err()
		case <-call.done:
			return call.item, call.err
		}
	}
	if time.Now().Before(s.currentCooldown) {
		s.mu.Unlock()
		return remote.Item{}, errors.New("本地平台请求处于冷却期。")
	}
	call := &currentCall{done: make(chan struct{})}
	s.currentCalls[key] = call
	s.mu.Unlock()
	data, err := s.current.Current(ctx, kind, id)
	item := remote.Item{}
	if err == nil {
		if data.Kind != kind || data.ID != id || data.Name == "" || data.Metric != nil && *data.Metric < 0 {
			err = errors.New("本地平台资料身份或数量无效。")
		} else {
			item = remote.Item{Kind: kind, ID: id, Name: data.Name, Cover: remote.NormalizeImageURL(data.Cover), OwnerID: data.OwnerID, OwnerName: data.OwnerName, Metric: data.Metric, PublishedAt: data.PublishedAt, MetadataSource: "bilibili", MetadataAt: data.At}
		}
	}
	s.mu.Lock()
	if err == nil {
		if len(s.currentCache) >= 2000 {
			for old := range s.currentCache {
				delete(s.currentCache, old)
				break
			}
		}
		s.currentCache[key] = currentRecord{item: item, until: time.Now().Add(time.Minute)}
	} else {
		var upstream *bilibili.Error
		if errors.As(err, &upstream) && (upstream.Code == "rate_limited" || upstream.Code == "restricted") {
			s.currentCooldown = time.Now().Add(max(time.Minute, upstream.RetryAfter))
		}
	}
	call.item, call.err = item, err
	delete(s.currentCalls, key)
	close(call.done)
	s.mu.Unlock()
	return item, err
}

func mergeCurrent(seed, current remote.Item) remote.Item {
	seed.Kind, seed.ID = current.Kind, current.ID
	seed.Name, seed.Cover, seed.OwnerID, seed.OwnerName = current.Name, current.Cover, current.OwnerID, current.OwnerName
	seed.Metric, seed.PublishedAt = current.Metric, current.PublishedAt
	seed.MetadataSource, seed.MetadataAt = current.MetadataSource, current.MetadataAt
	seed.MetadataError = current.MetadataError
	if current.MetadataSource != "bilibili" && current.Seq >= seed.Seq {
		seed.Seq = current.Seq
		seed.ObservedAt = max(seed.ObservedAt, current.ObservedAt)
		seed.Recorded = current.Recorded
	}
	seed.VideoViews, seed.RecentVideos = nil, nil
	seed.VideoCount = nil
	return seed
}

// mergeFallback 只补本地未获得的字段；当前版本号与服务器历史身份不由B站响应推断。
func mergeFallback(item, fallback remote.Item) remote.Item {
	if item.ID == "" {
		item = fallback
		item.MetadataSource = "server"
		item.MetadataAt = fallback.ObservedAt
	} else {
		mixed := false
		if item.Metric == nil && fallback.Metric != nil {
			item.Metric = fallback.Metric
			mixed = true
		}
		if item.Cover == "" && fallback.Cover != "" {
			item.Cover = fallback.Cover
			mixed = true
		}
		if mixed {
			item.MetadataSource = "bilibili+server"
		}
		item.Seq, item.ObservedAt, item.Recorded = fallback.Seq, fallback.ObservedAt, fallback.Recorded
	}
	item.Cover = remote.NormalizeImageURL(item.Cover)
	return item
}

func (s *Service) currentOrFallback(ctx context.Context, base, kind, id string) (remote.Item, error) {
	item, err := s.readCurrent(ctx, kind, id)
	if err == nil && item.Metric != nil && item.Cover != "" {
		return item, nil
	}
	fallback, readErr := s.client.Metadata(ctx, base, kind, id)
	if readErr == nil {
		return mergeFallback(item, fallback), nil
	}
	if err == nil {
		return item, nil
	}
	return remote.Item{}, readErr
}

func growth7(item *remote.Item, points []remote.Point) {
	item.Growth, item.GrowthFrom, item.GrowthTo = nil, 0, 0
	since, now := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6).UnixMilli(), time.Now().UnixMilli()
	filtered := []remote.Point{}
	for _, p := range points {
		if p.At >= since && p.At <= now {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return
	}
	item.GrowthFrom, item.GrowthTo = filtered[0].At, filtered[len(filtered)-1].At
	if len(filtered) > 1 {
		value := filtered[len(filtered)-1].Value - filtered[0].Value
		item.Growth = &value
	}
}

// currentItems 不查询跟踪状态；后台核对继续由独立队列承担。
func (s *Service) currentItems(ctx context.Context, base, kind string, ids []string, seeds map[string]remote.Item) (map[string]remote.Item, error) {
	localCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	jobs := make(chan string, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	items := map[string]remote.Item{}
	var lock sync.Mutex
	var workers sync.WaitGroup
	for range min(4, len(ids)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				item, err := s.readCurrent(localCtx, kind, id)
				if err == nil {
					lock.Lock()
					items[id] = item
					lock.Unlock()
				}
			}
		}()
	}
	workers.Wait()
	fallbackIDs := []string{}
	for _, id := range ids {
		item, ok := items[id]
		if !ok || item.Metric == nil || item.Cover == "" {
			fallbackIDs = append(fallbackIDs, id)
		}
	}
	var history map[string]remote.BatchMetric
	var historyErr error
	if base != "" {
		history, historyErr = s.client.MetricsBatch(ctx, base, kind, ids, fallbackIDs)
	} else {
		historyErr = errors.New("未配置历史服务器，当前只显示本地资料。")
	}
	result := map[string]remote.Item{}
	missing := false
	for _, id := range ids {
		current, hasCurrent := items[id]
		if stat, ok := history[id]; ok && stat.Fallback != nil {
			current = mergeFallback(current, *stat.Fallback)
			hasCurrent = true
		}
		seed := seeds[id]
		if hasCurrent {
			seed = mergeCurrent(seed, current)
		} else if seed.ID == "" {
			missing = true
			continue
		}
		if !hasCurrent {
			missing = true
			seed.MetadataError = "当前资料暂未更新，已保留本地缓存。"
		}
		if stat, ok := history[id]; ok {
			growth7(&seed, stat.Points)
			seed.HistoryError = ""
			if stat.Truncated {
				seed.Growth = nil
				seed.HistoryError = "历史样本超过上限，暂不展示变化值。"
			}
			if len(stat.Points) > 0 {
				seed.Recorded = true
			}
		} else {
			seed.HistoryError = "历史变化暂未更新，已保留最近记录。"
		}
		result[id] = seed
	}
	if historyErr != nil {
		return result, historyErr
	}
	if missing {
		return result, errors.New("部分目标当前资料暂未获取，已保留本地缓存。")
	}
	return result, nil
}

func (s *Service) syncLocalSummaries(ctx context.Context, kind string, ids []string) (int, error) {
	if kind != "video" && kind != "creator" {
		return 0, &Error{Message: "资料类型无效。", Status: 400}
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	if len(ids) == 0 {
		for _, f := range s.state.Follows {
			if f.Kind == kind {
				ids = append(ids, f.ID)
				if len(ids) == 24 {
					break
				}
			}
		}
	}
	if len(ids) > 24 {
		s.mu.Unlock()
		return 0, &Error{Message: "每轮最多同步24项。", Status: 400}
	}
	requested := map[string]bool{}
	for _, id := range ids {
		normalized, e := Normalize(kind, id)
		if e != nil || normalized != id || requested[id] {
			s.mu.Unlock()
			return 0, &Error{Message: "目标标识无效或重复。", Status: 400}
		}
		requested[id] = true
	}
	targets, seeds := map[string]Follow{}, map[string]remote.Item{}
	for _, f := range s.state.Follows {
		if f.Kind == kind && requested[f.ID] {
			targets[f.ID] = f
			seeds[f.ID] = followItem(f)
		}
	}
	selected := []string{}
	for _, id := range ids {
		if _, ok := targets[id]; ok {
			selected = append(selected, id)
		}
	}
	if len(selected) == 0 {
		s.mu.Unlock()
		return 0, nil
	}
	keyIDs := append([]string{}, selected...)
	sort.Strings(keyIDs)
	key := base + "|" + strconv.FormatUint(epoch, 10) + "|" + kind + "|" + strings.Join(keyIDs, ",")
	if call := s.summaryCalls[key]; call != nil {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.count, call.err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	call := &summaryCall{done: make(chan struct{})}
	s.summaryCalls[key] = call
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, readErr := s.currentItems(ctx, base, kind, selected, seeds)
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		readErr = &Error{Message: "服务器已切换，请重新读取。", Status: 409}
	} else {
		next := s.copyState()
		now := time.Now().UTC()
		for index, f := range next.Follows {
			original, ok := targets[f.ID]
			item, found := items[f.ID]
			if f.Kind == kind && ok && found && sameFollow(f, original) && setSummary(&next.Follows[index], item, base, now) {
				call.count++
			}
		}
		if call.count > 0 {
			if err := s.save(next); err != nil {
				call.count = 0
				readErr = err
			}
		}
	}
	call.err = readErr
	delete(s.summaryCalls, key)
	close(call.done)
	return call.count, call.err
}

func (s *Service) localDetail(ctx context.Context, base, kind, id string, page int, seq int64, days string) (remote.Detail, error) {
	if seq > 0 {
		return s.client.History(ctx, base, kind, id, page, seq, days)
	}
	if days != "" && days != "7" && days != "30" && days != "90" && days != "all" || page < 1 || page > 100000 {
		return remote.Detail{}, &Error{Message: "历史查询范围或页码无效。", Status: 400}
	}
	seed := remote.Item{Kind: kind, ID: id}
	s.mu.Lock()
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id {
			seed = followItem(f)
			break
		}
	}
	s.mu.Unlock()
	current, currentErr := s.currentOrFallback(ctx, base, kind, id)
	data, readErr := s.client.History(ctx, base, kind, id, page, 0, days)
	if readErr != nil {
		data.Item = seed
	}
	if currentErr == nil {
		data.Item = mergeCurrent(data.Item, current)
	} else {
		head := data.Item
		data.Item = seed
		if readErr == nil {
			data.Item.Seq, data.Item.ObservedAt, data.Item.Recorded = head.Seq, head.ObservedAt, head.Recorded
		}
		data.Item.MetadataError = "当前资料暂未更新，已保留本地缓存。"
	}
	if readErr == nil {
		growth7(&data.Item, data.Metrics.Points)
		return data, nil
	}
	return data, readErr
}
