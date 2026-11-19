// detail_sources.go 编排平台当前资料、服务器历史与只读资料回退，缓存分别按来源隔离。
// 网络在锁外；回写核对服务器代际和关注身份。见 docs/client-detail-data-sources.md。
package library

import (
	"context"
	"maps"
	"strconv"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

// MetadataReader 只查询公开单目标资料；必须并发安全并遵守ctx，不接收Cookie或负责服务器跟踪。
type MetadataReader interface {
	Current(context.Context, string, string) (bilibili.Metadata, error)
}

// CurrentView 的 Item.Seq 仅在 Source=server 时绑定服务器版本。
type CurrentView struct {
	Available      bool        `json:"available"`
	Item           remote.Item `json:"item"`
	Description    string      `json:"description"`
	Source         string      `json:"source"`
	ServerURL      string      `json:"server_url,omitempty"`
	SnapshotAt     int64       `json:"snapshot_at,omitempty"`
	CheckedAt      int64       `json:"checked_at"`
	Cached         bool        `json:"cached"`
	FallbackReason string      `json:"fallback_reason,omitempty"`
	ReadError      string      `json:"read_error,omitempty"`
	CacheError     string      `json:"cache_error,omitempty"`
	LocalFollow    *Follow     `json:"local_follow,omitempty"`
}
type HistoryView struct {
	remote.History
	ServerURL  string `json:"server_url"`
	CheckedAt  int64  `json:"checked_at"`
	Cached     bool   `json:"cached"`
	ReadError  string `json:"read_error,omitempty"`
	CacheError string `json:"cache_error,omitempty"`
}
type currentCall struct {
	done chan struct{}
	view CurrentView
	err  error
}
type historyCall struct {
	done chan struct{}
	view HistoryView
	err  error
}
type currentFailure struct {
	until time.Time
	view  CurrentView
}

func targetKey(kind, id string) string { return kind + ":" + id }
func (s *Service) followIdentityLocked(kind, id string) string {
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id {
			return f.Identity
		}
	}
	return ""
}
func (s *Service) decorateCurrentLocked(v CurrentView) CurrentView {
	v.LocalFollow = nil
	for _, f := range s.state.Follows {
		if f.Kind == v.Item.Kind && f.ID == v.Item.ID {
			f.Data = nil
			f.Current = nil
			v.LocalFollow = &f
			break
		}
	}
	return v
}

// cachedCurrentLocked 兼容升级前同服务器的摘要/默认详情，不把旧快照当作平台当前观测。
func (s *Service) cachedCurrentLocked(kind, id string) (CurrentView, bool) {
	if v, ok := s.state.Current[targetKey(kind, id)]; ok && (v.Source == "platform" || v.Source == "server" && v.ServerURL == s.state.ServerURL) {
		return v, true
	}
	for _, f := range s.state.Follows {
		if f.Kind != kind || f.ID != id || f.ServerURL != s.state.ServerURL {
			continue
		}
		var item remote.Item
		if f.Summary != nil {
			item = *f.Summary
		} else if f.Data != nil {
			item = f.Data.Item
		} else {
			continue
		}
		if item.Kind != kind || item.ID != id || !item.Recorded || item.Seq < 1 {
			continue
		}
		checked := item.ObservedAt
		if f.UpdatedAt != nil {
			checked = f.UpdatedAt.UnixMilli()
		}
		if f.SummaryAt != nil {
			checked = f.SummaryAt.UnixMilli()
		}
		item.Cover = remote.NormalizeImageURL(item.Cover)
		return CurrentView{Available: true, Item: item, Source: "server", ServerURL: f.ServerURL, CheckedAt: checked, Cached: true}, true
	}
	return CurrentView{Item: remote.Item{Kind: kind, ID: id}}, false
}

func (s *Service) cachedHistoryLocked(kind, id, days string) (HistoryView, bool) {
	if v, ok := s.state.History[targetKey(kind, id)+":"+days]; ok && v.ServerURL == s.state.ServerURL {
		return v, true
	}
	if days != "30" {
		return HistoryView{}, false
	}
	for _, f := range s.state.Follows {
		if f.Kind != kind || f.ID != id || f.ServerURL != s.state.ServerURL || f.Data == nil {
			continue
		}
		i := f.Data.Item
		if i.Kind != kind || i.ID != id || !i.Recorded || i.Seq < 1 {
			continue
		}
		target := remote.Status{Kind: kind, ID: id, Recorded: true, Tracking: i.Tracking, TrackingState: "absent", State: i.Status}
		if i.Tracking {
			target.TrackingState = "active"
		} else if i.Status == "paused" {
			target.TrackingState = "paused"
		}
		if f.Data.Target != nil {
			target = *f.Data.Target
		}
		checked := i.ObservedAt
		if f.UpdatedAt != nil {
			checked = f.UpdatedAt.UnixMilli()
		}
		return HistoryView{History: remote.History{Record: remote.Record{Kind: kind, ID: id, Seq: i.Seq, ObservedAt: i.ObservedAt, Recorded: true}, Target: target, Metrics: remote.Series{Points: f.Data.Metrics.Points, Truncated: f.Data.Metrics.Truncated}}, ServerURL: f.ServerURL, CheckedAt: checked, Cached: true}, true
	}
	return HistoryView{}, false
}

// CurrentCache 从不发出网络请求；缓存可先于平台或历史分支显示。
func (s *Service) CurrentCache(kind, input string) (CurrentView, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return CurrentView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, _ := s.cachedCurrentLocked(kind, id)
	v.Cached = v.Available
	return s.decorateCurrentLocked(v), nil
}

// Current 先使用有效缓存；平台失败才查服务器资料，所有来源失败时保留旧缓存。
// refresh 只绕过成功缓存 TTL，不绕过失败退避和在途合并。
func (s *Service) Current(ctx context.Context, kind, input string, refresh bool) (CurrentView, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return CurrentView{}, err
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	identity := s.followIdentityLocked(kind, id)
	key := targetKey(kind, id)
	callKey := key + ":" + strconv.FormatUint(epoch, 10) + ":" + identity
	if call := s.currentCalls[callKey]; call != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return CurrentView{}, ctx.Err()
		case <-call.done:
			return call.view, call.err
		}
	}
	now := time.Now()
	if f, ok := s.currentFailures[callKey]; ok && now.Before(f.until) {
		v := s.decorateCurrentLocked(f.view)
		v.Cached = v.Available
		s.mu.Unlock()
		return v, nil
	}
	old, ok := s.cachedCurrentLocked(kind, id)
	ttl := 5 * time.Minute
	if old.Source == "server" {
		ttl = time.Minute
	}
	if ok && old.ReadError == "" && !refresh && now.UnixMilli()-old.CheckedAt < ttl.Milliseconds() {
		old.Cached = true
		old = s.decorateCurrentLocked(old)
		s.mu.Unlock()
		return old, nil
	}
	call := &currentCall{done: make(chan struct{})}
	s.currentCalls[callKey] = call
	s.mu.Unlock()
	v := CurrentView{Item: remote.Item{Kind: kind, ID: id}, CheckedAt: now.UnixMilli()}
	platformCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	data, platformErr := s.platform.Current(platformCtx, kind, id)
	cancel()
	if platformErr == nil && (data.Kind != kind || data.ID != id || data.Name == "" || data.ObservedAt < 1 || data.Metric != nil && *data.Metric < 0) {
		platformErr = &Error{Status: 502, Message: "平台资料身份无效。"}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if platformErr == nil {
		v.Available = true
		v.Source = "platform"
		v.Description = data.Description
		v.Item = remote.Item{Kind: kind, ID: id, Name: data.Name, Cover: remote.NormalizeImageURL(data.Cover), Metric: data.Metric, OwnerID: data.OwnerID, OwnerName: data.OwnerName, PublishedAt: data.PublishedAt, ObservedAt: data.ObservedAt}
	} else {
		v.FallbackReason = platformErr.Error()
		fallbackCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		saved, savedErr := s.client.Saved(fallbackCtx, base, kind, id)
		stop()
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if savedErr == nil {
			v.Available = true
			v.Source = "server"
			v.ServerURL = base
			v.Item = saved.Item
			v.Description = saved.Description
			v.SnapshotAt = saved.SnapshotAt
			v.Item.Cover = remote.NormalizeImageURL(v.Item.Cover)
		} else {
			if ok {
				v = old
				v.Cached = true
				v.CheckedAt = now.UnixMilli()
			}
			v.FallbackReason = platformErr.Error()
			v.ReadError = "平台资料无法读取；服务器回退失败：" + savedErr.Error()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { call.view = v; call.err = err; delete(s.currentCalls, callKey); close(call.done) }()
	if base != s.state.ServerURL || epoch != s.serverEpoch || identity != s.followIdentityLocked(kind, id) {
		err = &Error{Status: 409, Message: "详情上下文已改变，请重新读取。"}
		return CurrentView{}, err
	}
	if err != nil {
		return CurrentView{}, err
	}
	// 不保存本地关注副本；避免递归缓存与关注身份过期。
	v.LocalFollow = nil
	if v.Available {
		next := s.copyState()
		next.Current = maps.Clone(s.state.Current)
		if next.Current == nil {
			next.Current = map[string]CurrentView{}
		}
		if len(next.Current) >= 2000 {
			for k := range next.Current {
				delete(next.Current, k)
				break
			}
		}
		next.Current[key] = v
		if e := s.save(next); e != nil {
			v.CacheError = e.Error()
		}
	}
	if platformErr != nil {
		s.currentFailures[callKey] = currentFailure{until: time.Now().Add(time.Minute), view: v}
	} else {
		delete(s.currentFailures, callKey)
	}
	if len(s.currentFailures) > 2000 {
		for k, f := range s.currentFailures {
			if time.Now().After(f.until) {
				delete(s.currentFailures, k)
			}
		}
		if len(s.currentFailures) > 2000 {
			for k := range s.currentFailures {
				if k != callKey {
					delete(s.currentFailures, k)
					break
				}
			}
		}
	}
	v = s.decorateCurrentLocked(v)
	return v, nil
}

// History 只读取独立服务器历史，不查询平台、核对任务或申请跟踪；失败仅回退同来源同范围缓存。
func (s *Service) History(ctx context.Context, kind, input, days string) (HistoryView, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return HistoryView{}, err
	}
	if !remote.ValidDays(days) {
		return HistoryView{}, &Error{Status: 400, Message: "时间范围无效。"}
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	key := targetKey(kind, id) + ":" + days
	callKey := key + ":" + strconv.FormatUint(epoch, 10)
	if call := s.historyCalls[callKey]; call != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return HistoryView{}, ctx.Err()
		case <-call.done:
			return call.view, call.err
		}
	}
	call := &historyCall{done: make(chan struct{})}
	s.historyCalls[callKey] = call
	s.mu.Unlock()
	data, readErr := s.client.History(ctx, base, kind, id, days)
	v := HistoryView{History: data, ServerURL: base, CheckedAt: time.Now().UnixMilli()}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { call.view = v; call.err = err; delete(s.historyCalls, callKey); close(call.done) }()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		err = &Error{Status: 409, Message: "服务器已切换，请重新读取。"}
		return HistoryView{}, err
	}
	if ctx.Err() != nil {
		err = ctx.Err()
		return HistoryView{}, err
	}
	if readErr != nil {
		if old, ok := s.cachedHistoryLocked(kind, id, days); ok {
			v = old
			v.Cached = true
			v.ReadError = readErr.Error()
			return v, nil
		}
		err = readErr
		return HistoryView{}, err
	}
	next := s.copyState()
	next.History = maps.Clone(s.state.History)
	if next.History == nil {
		next.History = map[string]HistoryView{}
	}
	if len(next.History) >= 200 {
		for k := range next.History {
			delete(next.History, k)
			break
		}
	}
	next.History[key] = v
	if e := s.save(next); e != nil {
		v.CacheError = e.Error()
	}
	return v, nil
}

func (s *Service) Versions(ctx context.Context, kind, input string, before int64) (remote.VersionCursor, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return remote.VersionCursor{}, err
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	s.mu.Unlock()
	v, err := s.client.Versions(ctx, base, kind, id, before)
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		return remote.VersionCursor{}, &Error{Status: 409, Message: "服务器已切换。"}
	}
	return v, err
}
func (s *Service) Snapshot(ctx context.Context, kind, input string, seq int64) (remote.VersionSnapshot, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return remote.VersionSnapshot{}, err
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	s.mu.Unlock()
	v, err := s.client.Snapshot(ctx, base, kind, id, seq)
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		return remote.VersionSnapshot{}, &Error{Status: 409, Message: "服务器已切换。"}
	}
	return v, err
}
