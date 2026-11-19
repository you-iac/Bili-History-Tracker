// Package library 管理本地私人 UP/视频关注、服务器地址与最近读取的公共资料。
// 写入成功才更新内存；网络操作在锁外执行，取消关注不会请求服务器停采。见 docs/client-library.md。
// 摘要同步独立于关注提交，范围匹配的详情才可作为离线缓存。
// 浏览/后台核对、JSON业务失败和有界重试见 docs/public-response.md。
// 自动关注、公开查询和退避队列见 docs/client-workspace.md；独立详情与匹配缓存见 docs/client-catalog.md；图片地址缓存见 docs/client-launch-media.md。
// 正常Agent注入CurrentSource，当前资料本地查询、历史批读与UP本地汇总见 docs/local-first-metadata.md。
package library

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

type Error struct {
	Message string
	Status  int
}

// 核对包含状态8秒、提交55秒与节流余量；刷新另含一次资料读取，见 docs/request-stages.md。
const (
	reconcileTimeout = 65 * time.Second
	refreshTimeout   = 90 * time.Second
)

func (e *Error) Error() string { return e.Message }

type Storage interface {
	LoadLibrary(any) error
	SaveLibrary(any) error
}
type Follow struct {
	Identity         string `json:"follow_identity,omitempty"`
	epoch            uint64
	CheckedAt        *time.Time      `json:"checked_at,omitempty"`
	LastCode         string          `json:"last_code,omitempty"`
	LastStage        string          `json:"last_stage,omitempty"`
	LastOutcome      string          `json:"last_outcome,omitempty"`
	NeedsAttention   bool            `json:"needs_attention,omitempty"`
	Summary          *remote.Item    `json:"summary,omitempty"`
	SummaryAt        *time.Time      `json:"summary_at,omitempty"`
	DiscoveryOwnerID string          `json:"discovery_creator_id,omitempty"`
	Discovery        *bilibili.Video `json:"discovery,omitempty"`
	Kind             string          `json:"kind"`
	ID               string          `json:"id"`
	AddedAt          time.Time       `json:"added_at"`
	Status           *remote.Status  `json:"remote,omitempty"`
	Data             *remote.Detail  `json:"data,omitempty"`
	ServerURL        string          `json:"server_url,omitempty"`
	UpdatedAt        *time.Time      `json:"updated_at,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
	Pending          bool            `json:"pending_submission"`
	Attempts         int             `json:"submission_attempts,omitempty"`
	NextAttempt      *time.Time      `json:"next_attempt_at,omitempty"`
}
type state struct {
	Images    map[string]ImageRecord `json:"images,omitempty"`
	Version   int                    `json:"version"`
	ServerURL string                 `json:"server_url"`
	Follows   []Follow               `json:"follows"`
	Overview  *remote.Overview       `json:"overview,omitempty"`
}
type Page struct {
	Items    []Follow `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	Limit    int      `json:"limit"`
	Creators int      `json:"creators"`
	Videos   int      `json:"videos"`
}
type Service struct {
	current         CurrentSource
	currentCache    map[string]currentRecord
	currentCalls    map[string]*currentCall
	currentCooldown time.Time
	summaryCalls    map[string]*summaryCall
	serverEpoch     uint64
	imageCalls      map[string]*imageCall
	imageErrors     map[string]imageFailure
	imageSlots      chan struct{}
	mu              sync.Mutex
	store           Storage
	client          *remote.Client
	state           state
	inFlight        map[string]bool
	wake            chan struct{}
}

// New 可注入当前资料源；命令入口始终传入scanner，省略时保留旧接入方的服务器读取行为。
func New(store Storage, client *remote.Client, sources ...CurrentSource) (*Service, error) {
	s := &Service{store: store, client: client, state: state{Version: 1, Follows: []Follow{}}, inFlight: map[string]bool{}, wake: make(chan struct{}, 1), imageCalls: map[string]*imageCall{}, imageErrors: map[string]imageFailure{}, imageSlots: make(chan struct{}, 4), summaryCalls: map[string]*summaryCall{}}
	s.currentCache = map[string]currentRecord{}
	s.currentCalls = map[string]*currentCall{}
	if len(sources) > 0 {
		s.current = sources[0]
	}
	if err := store.LoadLibrary(&s.state); err != nil {
		return nil, errors.New("关注库无法读取，请先备份 library.json 后检查文件")
	}
	if s.state.Overview != nil && (!s.state.Overview.CatalogBased || s.state.Overview.CreatorLimit < 1 || s.state.Overview.VideoLimit < 1) {
		s.state.Overview = nil
	}
	base, err := remote.NormalizeBase(s.state.ServerURL)
	if err != nil || base != s.state.ServerURL || s.state.Version != 1 || len(s.state.Follows) > 10000 {
		return nil, errors.New("本地关注库格式无效，请备份 library.json 后检查")
	}
	seen := map[string]bool{}
	for i, f := range s.state.Follows {
		id, e := Normalize(f.Kind, f.ID)
		key := f.Kind + ":" + f.ID
		if e != nil || id != f.ID || seen[key] || f.AddedAt.IsZero() || f.Attempts < 0 {
			return nil, errors.New("本地关注标识或格式无效")
		}
		seen[key] = true
		if f.Identity == "" {
			s.state.Follows[i].Identity = rand.Text()
		}
		// 每次启动重新核对旧确认，但保留失败退避和人工处理状态。
		s.state.Follows[i].CheckedAt = nil
		if f.Status == nil || !f.Status.Tracking {
			s.state.Follows[i].Pending = f.Status == nil || f.Status.State != "paused" && f.Status.TrackingState != "paused"
		}
	}
	return s, nil
}

var bvid = regexp.MustCompile(`^BV[1-9A-HJ-NP-Za-km-z]{10}$`)

func Normalize(kind, input string) (string, error) {
	if kind == "creator" {
		id, err := bilibili.CreatorID(input)
		if err == nil {
			return id, nil
		}
	}
	if kind == "video" {
		id := strings.TrimSpace(input)
		if strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "http://") {
			u, err := url.Parse(id)
			if err == nil && u.User == nil && u.Port() == "" && (u.Hostname() == "www.bilibili.com" || u.Hostname() == "bilibili.com" || u.Hostname() == "m.bilibili.com") {
				parts := strings.Split(strings.Trim(u.Path, "/"), "/")
				if len(parts) == 2 && parts[0] == "video" {
					id = parts[1]
				}
			}
		}
		if bvid.MatchString(id) {
			return id, nil
		}
	}
	return "", &Error{Message: "UP 请填写 UID 或空间链接，视频请填写 BV 号或标准视频链接。", Status: 400}
}
func (s *Service) copyState() state {
	next := s.state
	next.Follows = append([]Follow{}, s.state.Follows...)
	return next
}
func (s *Service) save(next state) error {
	if err := s.store.SaveLibrary(next); err != nil {
		return &Error{Message: "本地关注库保存失败，原数据未更改。", Status: 500}
	}
	s.state = next
	return nil
}
func (s *Service) Connection() string { s.mu.Lock(); defer s.mu.Unlock(); return s.state.ServerURL }

// Configure 更换服务器时清除旧公共缓存，防止把不同服务器的历史混合。
func (s *Service) Configure(input string) error {
	base, err := remote.NormalizeBase(input)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if base == s.state.ServerURL {
		return nil
	}
	next := s.copyState()
	next.ServerURL = base
	next.Overview = nil
	next.Images = nil
	for i := range next.Follows {
		var localCurrent *remote.Item
		if s.current != nil && next.Follows[i].Summary != nil && next.Follows[i].Summary.MetadataSource == "bilibili" {
			item := *next.Follows[i].Summary
			item.Growth = nil
			item.GrowthFrom, item.GrowthTo, item.Seq, item.ObservedAt = 0, 0, 0, 0
			item.Recorded, item.Tracking = false, false
			item.Status = ""
			item.HistoryError = "服务器已切换，等待更新历史变化。"
			localCurrent = &item
		}
		next.Follows[i].Summary = nil
		if localCurrent != nil {
			next.Follows[i].Summary = localCurrent
		}
		next.Follows[i].SummaryAt = nil
		next.Follows[i].Status = nil
		next.Follows[i].Data = nil
		next.Follows[i].ServerURL = ""
		next.Follows[i].UpdatedAt = nil
		next.Follows[i].LastError = ""
		next.Follows[i].Pending = true
		next.Follows[i].Attempts = 0
		next.Follows[i].NextAttempt = nil
		next.Follows[i].CheckedAt = nil
		next.Follows[i].LastCode = ""
		next.Follows[i].LastStage = ""
		next.Follows[i].LastOutcome = ""
		next.Follows[i].NeedsAttention = false
	}
	err = s.save(next)
	if err == nil {
		s.serverEpoch++
		clear(s.imageErrors)
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return err
}
func (s *Service) Test(ctx context.Context) error { return s.client.Test(ctx, s.Connection()) }
func (s *Service) Add(kind, input string) (Follow, error) {
	id, err := Normalize(kind, input)
	if err != nil {
		return Follow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id {
			return f, nil
		}
	}
	if len(s.state.Follows) >= 10000 {
		return Follow{}, &Error{Message: "本地关注已达10000项上限。", Status: 409}
	}
	f := Follow{Identity: rand.Text(), Kind: kind, ID: id, AddedAt: time.Now().UTC(), Pending: true}
	next := s.copyState()
	next.Follows = append(next.Follows, f)
	return f, s.save(next)
}
func (s *Service) Remove(kind, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	for i, f := range next.Follows {
		if f.Kind == kind && f.ID == id {
			next.Follows = append(next.Follows[:i], next.Follows[i+1:]...)
			return s.save(next)
		}
	}
	return nil
}
func (s *Service) List(kind, q string, page int) Page {
	return s.ListFiltered(kind, q, page, false)
}

func (s *Service) ListFiltered(kind, q string, page int, pendingOnly bool) Page {
	return s.ListCatalog(kind, q, page, pendingOnly, "", "")
}
func (s *Service) find(kind, id string) (Follow, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id {
			f.epoch = s.serverEpoch
			return f, s.state.ServerURL, nil
		}
	}
	return Follow{}, "", &Error{Message: "本地尚未关注此目标。", Status: 404}
}
func (s *Service) update(original Follow, base string, change func(*Follow)) (Follow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || original.epoch != s.serverEpoch {
		return Follow{}, &Error{Message: "服务器配置已更改，请重新刷新。", Status: 409}
	}
	next := s.copyState()
	for i, f := range next.Follows {
		if f.Kind == original.Kind && f.ID == original.ID && sameFollow(f, original) {
			change(&next.Follows[i])
			err := s.save(next)
			return next.Follows[i], err
		}
	}
	return Follow{}, &Error{Message: "该项已在本地取消关注。", Status: 404}
}
func (s *Service) Refresh(ctx context.Context, kind, id string) (Follow, error) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	if s.current != nil {
		_, readErr := s.SyncSummaries(ctx, kind, id)
		f, _, findErr := s.find(kind, id)
		if findErr != nil {
			return f, findErr
		}
		return f, readErr
	}
	f, base, err := s.find(kind, id)
	if err != nil {
		return f, err
	}
	checked, err := s.Reconcile(ctx, kind, id, true)
	if err != nil {
		return checked, err
	}
	if checked.Status == nil {
		return checked, nil
	}
	status := *checked.Status
	var detail *remote.Detail
	if err == nil && status.Recorded {
		value, e := s.client.Detail(ctx, base, kind, id, 1, 0)
		detail = &value
		err = e
	}
	if err != nil {
		_, _ = s.update(f, base, func(v *Follow) { v.LastError = err.Error() })
		return f, err
	}
	return s.update(f, base, func(v *Follow) {
		now := time.Now().UTC()
		v.Status = &status
		if detail != nil {
			v.Data = detail
			item := detail.Item
			v.Summary = &item
			v.SummaryAt = &now
		}
		v.ServerURL = base
		v.UpdatedAt = &now
		if status.TrackingState != "paused" && status.State != "paused" {
			v.LastError = ""
		}
	})
}

// Submit 仅提交已保存的本地关注；远端失败不回滚个人关注。
func (s *Service) Submit(ctx context.Context, kind, id string) (Follow, error) {
	return s.Reconcile(ctx, kind, id, true)
}

// Follow 始终先保存本地再申请公共跟踪；远程失败返回成功保存的关注和待提交状态。
// HTTP 调用方不能通过可选参数绕过提交；本地存储失败仍返回错误。
func (s *Service) Follow(ctx context.Context, kind, input string) (Follow, error) {
	f, err := s.Add(kind, input)
	if err != nil {
		return f, err
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	updated, err := s.Submit(ctx, f.Kind, f.ID)
	if err == nil {
		return updated, nil
	}
	var local *Error
	if errors.As(err, &local) && local.Status != 409 {
		return f, err
	}
	f, _, readErr := s.find(f.Kind, f.ID)
	return f, readErr
}

type PublicItem struct {
	remote.Item
	Followed bool `json:"followed"`
}
type PublicPage struct {
	Items []PublicItem `json:"items"`
	Page  int          `json:"page"`
	Limit int          `json:"limit"`
	Total int          `json:"total"`
}

func (s *Service) Search(ctx context.Context, kind, q string, page int, options ...remote.CatalogOptions) (PublicPage, error) {
	id := ""
	if normalized, err := Normalize(kind, q); err == nil {
		id = normalized
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	s.mu.Unlock()
	if s.current != nil {
		if len(options) == 0 {
			options = []remote.CatalogOptions{{Identity: true}}
		} else {
			options[0].Identity = true
		}
	}
	data, err := s.client.Catalog(ctx, base, kind, q, id, page, options...)
	if err != nil {
		return PublicPage{}, err
	}
	if s.current != nil && len(data.Items) > 0 {
		ids, seeds := []string{}, map[string]remote.Item{}
		for _, item := range data.Items {
			ids = append(ids, item.ID)
			seeds[item.ID] = item
		}
		fresh, _ := s.currentItems(ctx, base, kind, ids, seeds)
		for index, item := range data.Items {
			if updated, ok := fresh[item.ID]; ok {
				data.Items[index] = updated
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		return PublicPage{}, &Error{Message: "服务器已切换，请重新搜索。", Status: 409}
	}
	followed := map[string]bool{}
	stats := map[string]creatorStats{}
	if s.current != nil {
		stats = localCreatorStats(s.state.Follows, time.Now())
	}
	for _, f := range s.state.Follows {
		followed[f.Kind+":"+f.ID] = true
	}
	result := PublicPage{Items: []PublicItem{}, Page: data.Page, Limit: data.Limit, Total: data.Total}
	for _, item := range data.Items {
		if s.current != nil && kind == "creator" {
			item = applyLocalStats(item, stats)
		}
		result.Items = append(result.Items, PublicItem{Item: item, Followed: followed[item.Kind+":"+item.ID]})
	}
	return result, nil
}

type Dashboard struct {
	Creators  int              `json:"creators"`
	Videos    int              `json:"videos"`
	Pending   int              `json:"pending"`
	ServerURL string           `json:"server_url"`
	Public    *remote.Overview `json:"public,omitempty"`
}

func (s *Service) Dashboard() Dashboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Dashboard{ServerURL: s.state.ServerURL, Public: s.state.Overview}
	for _, f := range s.state.Follows {
		if f.Kind == "creator" {
			d.Creators++
		} else {
			d.Videos++
		}
		if f.Pending {
			d.Pending++
		}
	}
	return d
}
func (s *Service) RefreshOverview(ctx context.Context) (Dashboard, error) {
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	s.mu.Unlock()
	data, err := s.client.Overview(ctx, base, s.current != nil)
	if err != nil {
		return s.Dashboard(), err
	}
	if s.current != nil {
		for _, group := range []struct {
			kind  string
			items *[]remote.Item
		}{{"creator", &data.Creators}, {"video", &data.Videos}} {
			ids, seeds := []string{}, map[string]remote.Item{}
			for _, item := range *group.items {
				ids = append(ids, item.ID)
				seeds[item.ID] = item
			}
			if len(ids) > 0 {
				updated, _ := s.currentItems(ctx, base, group.kind, ids, seeds)
				for index, item := range *group.items {
					if fresh, ok := updated[item.ID]; ok {
						(*group.items)[index] = fresh
					}
				}
			}
		}
	}
	s.mu.Lock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		s.mu.Unlock()
		return Dashboard{}, &Error{Message: "服务器已切换，请刷新总览。", Status: 409}
	}
	next := s.copyState()
	next.Overview = &data
	err = s.save(next)
	s.mu.Unlock()
	return s.Dashboard(), err
}

// DetailView 附带本地关注与来源，不将公共跟踪状态当作个人关注。
type DetailView struct {
	remote.Detail
	LocalFollow *Follow `json:"local_follow,omitempty"`
	ServerURL   string  `json:"server_url"`
	Cached      bool    `json:"cached"`
	ReadError   string  `json:"read_error,omitempty"`
	ReadCode    string  `json:"read_code,omitempty"`
	CacheError  string  `json:"cache_error,omitempty"`
}

// Detail 复用既有服务器详情；仅默认30日第一页可在断连时回退本地缓存。
// 返回前重新核对服务器，取消关注/切服后不把旧请求写回或误标为仍关注。
func (s *Service) Detail(ctx context.Context, kind, id string, page int, seq int64, days string) (DetailView, error) {
	normalized, err := Normalize(kind, id)
	if err != nil {
		return DetailView{}, err
	}
	s.mu.Lock()
	base, epoch := s.state.ServerURL, s.serverEpoch
	var original *Follow
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == normalized {
			copy := f
			original = &copy
			break
		}
	}
	s.mu.Unlock()
	readStarted := time.Now()
	var data remote.Detail
	var readErr error
	if s.current != nil {
		data, readErr = s.localDetail(ctx, base, kind, normalized, page, seq, days)
	} else {
		data, readErr = s.client.Detail(ctx, base, kind, normalized, page, seq, days)
	}
	var failure *remote.Error
	if s.current == nil && original != nil && (data.Target != nil || errors.As(readErr, &failure) && failure.Code == "DETAIL_NOT_RECORDED") {
		// 新鲜服务器结果可推翻旧确认的TTL；失败后的退避与人工处理仍须遵守。
		hint := data.Target
		if failure != nil {
			hint = failure.Target
		}
		s.mu.Lock()
		currentRequest := base == s.state.ServerURL && epoch == s.serverEpoch
		stillFollowed := false
		for i, f := range s.state.Follows {
			if f.Kind == kind && f.ID == normalized && sameFollow(f, *original) {
				stillFollowed = true
				if currentRequest && hint != nil && f.Status != nil && f.LastError == "" && (f.Status.Tracking != hint.Tracking || f.Status.TrackingState != hint.TrackingState) {
					s.state.Follows[i].CheckedAt = nil
				}
				break
			}
		}
		s.mu.Unlock()
		if currentRequest && stillFollowed {
			checked, checkErr := s.Reconcile(ctx, kind, normalized, false)
			if checkErr == nil && readErr != nil && checked.Status != nil && checked.Status.Tracking && checked.Status.Recorded && seq == 0 {
				data, readErr = s.client.Detail(ctx, base, kind, normalized, page, seq, days)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL || epoch != s.serverEpoch {
		return DetailView{}, &Error{Message: "服务器已切换，请重新读取详情。", Status: 409}
	}
	view := DetailView{Detail: data, ServerURL: base}
	if data.Item.MetadataError != "" {
		view.ReadError = data.Item.MetadataError
	}
	if readErr == nil {
		if original != nil && (s.current == nil || seq == 0) {
			next := s.copyState()
			for index, f := range next.Follows {
				if f.Kind == kind && f.ID == normalized && sameFollow(f, *original) {
					now := time.Now().UTC()
					if setSummary(&next.Follows[index], data.Item, base, now) {
						if page == 1 && seq == 0 && (days == "" || days == "30") {
							copy := data
							next.Follows[index].Data = &copy
							next.Follows[index].UpdatedAt = &now
						}
						if err := s.save(next); err != nil {
							view.CacheError = err.Error()
						}
					}
					break
				}
			}
		}
		s.rememberSnapshotLocked(kind, normalized, data.SnapshotSeq, data.Snapshot)
	}
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == normalized {
			copy := f
			view.LocalFollow = &copy
			break
		}
	}
	if readErr != nil {
		if s.current != nil && seq == 0 && original != nil && data.Item.MetadataSource != "" {
			next := s.copyState()
			for index, f := range next.Follows {
				if f.Kind == kind && f.ID == normalized && sameFollow(f, *original) {
					if setSummary(&next.Follows[index], data.Item, base, time.Now().UTC()) {
						if err := s.save(next); err != nil {
							view.CacheError = err.Error()
						}
					}
					break
				}
			}
		}
		code := "REMOTE_ERROR"
		var remoteFailure *remote.Error
		if errors.As(readErr, &remoteFailure) {
			code = remoteFailure.Code
			view.Target = remoteFailure.Target
		}
		if view.LocalFollow != nil && view.LocalFollow.Status != nil && view.LocalFollow.CheckedAt != nil && (view.Target == nil || view.LocalFollow.CheckedAt.After(readStarted)) {
			view.Target = view.LocalFollow.Status
		}
		view.ReadCode = code
		f := view.LocalFollow
		if f != nil && f.Data != nil && f.ServerURL == base && page == 1 && seq == 0 && (days == "" || days == "30") {
			target := view.Target
			view.Detail = *f.Data
			if s.current != nil && data.Item.MetadataSource != "" {
				view.Item = mergeCurrent(view.Item, data.Item)
			}
			view.Target = target
			f.Data = nil
			view.Cached, view.ReadError = true, readErr.Error()
			return view, nil
		}
		if s.current != nil && seq == 0 && data.Item.MetadataSource != "" {
			view.Detail = data
			view.Metrics.Points = []remote.Point{}
			view.Versions = []remote.Version{}
			view.VersionPage = 1
			view.VersionLimit = 10
			view.ReadError = readErr.Error()
			return view, nil
		}
		if code == "DETAIL_NOT_RECORDED" && seq == 0 {
			view.Item = remote.Item{Kind: kind, ID: normalized, Status: "not_recorded"}
			if view.Target != nil {
				view.Item.Tracking = view.Target.Tracking
				view.Item.Status = view.Target.State
			}
			if f != nil {
				if f.Summary != nil {
					view.Item.Name = f.Summary.Name
					view.Item.OwnerName = f.Summary.OwnerName
				}
				f.Data = nil
			}
			view.ReadError = readErr.Error()
			return view, nil
		}
		return DetailView{}, readErr
	}
	if view.LocalFollow != nil {
		view.LocalFollow.Data = nil
	}
	return view, nil
}

// sameFollow使用随机身份区分取消后重新关注；时间戳在Windows同一时钟刻度内可能相同。
func sameFollow(a, b Follow) bool { return a.Identity == b.Identity && a.AddedAt.Equal(b.AddedAt) }
