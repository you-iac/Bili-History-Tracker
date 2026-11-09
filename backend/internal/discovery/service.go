// Package discovery 融合本地与平台作者视频，按当前关注关系过滤并提供统一续页。
// 会话与任务有容量、时限和取消约束，不解释平台合集或业务码。
// 只有显式提交才委托 tracker 写入关注。设计见 docs/video-discovery.md。
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

const pageSize = 30
const maxVideos = 10000
const maxSteps = 1000
const capacity = 64
const lifetime = 20 * time.Minute

var ErrExpired = errors.New("discovery session expired")
var ErrInput = errors.New("invalid discovery input")
var ErrCapacity = errors.New("discovery capacity exceeded")

// Reader 提供稳定本地续页和批量状态；未来可独立接入个人订阅。
type Reader interface {
	CreatorVideos(context.Context, string, string, string, int) ([]storage.CreatorVideoRecord, error)
	VideoStates(context.Context, string, []string) (map[string]storage.CreatorVideoRecord, error)
}

// Queries 提供平台无关的发现与经归属验证的单视频导入。
type Queries interface {
	GetCreatorVideoPage(context.Context, string, string, string) (source.CreatorVideoPage, error)
	IngestVideo(context.Context, string, string, time.Duration) (string, error)
}

// Service 可并发使用；服务退出时须 Close 取消全部任务。
type Service struct {
	mu         sync.Mutex
	reader     Reader
	queries    Queries
	platform   string
	ctx        context.Context
	cancel     context.CancelFunc
	sessions   map[string]*session
	jobs       map[string]*job
	keys       map[string]string
	importGate chan struct{}
}
type session struct {
	mu                          sync.Mutex
	id, creator                 string
	expires                     time.Time
	ctx                         context.Context
	cancel                      context.CancelFunc
	items                       []source.CreatorVideo
	known                       map[string]int
	localAfter, remoteCursor    string
	localDone, remoteDone, busy bool
	status, issue               string
	retryAt                     time.Time
	steps                       int
	changed                     chan struct{}
}

// Page 为空且 State=pending 时仍可续页，complete 只描述本次可发现范围。
type Page struct {
	Items      []storage.CreatorVideoRecord `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
	Session    string                       `json:"session,omitempty"`
	State      string                       `json:"state"`
	Issue      string                       `json:"issue,omitempty"`
	RetryAt    time.Time                    `json:"retry_at,omitempty"`
}

func New(reader Reader, queries Queries, platform string) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{reader: reader, queries: queries, platform: platform, ctx: ctx, cancel: cancel, sessions: map[string]*session{}, jobs: map[string]*job{}, keys: map[string]string{}, importGate: make(chan struct{}, 1)}
}

// Close 取消全部在途任务，不删除成功采集的历史。
func (s *Service) Close() { s.cancel() }
func token() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func valid(value string) bool {
	return value != "" && len(value) <= 200 && !strings.ContainsAny(value, "\x00\r\n/")
}
func (s *Service) cleanLocked() {
	now := time.Now()
	for id, v := range s.sessions {
		if now.After(v.expires) {
			v.cancel()
			delete(s.sessions, id)
		}
	}
	for id, v := range s.jobs {
		if now.After(v.expires) {
			v.cancel()
			delete(s.jobs, id)
		}
	}
	for key, id := range s.keys {
		if s.jobs[id] == nil {
			delete(s.keys, key)
		}
	}
}
func (s *Service) find(id, creator string) (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanLocked()
	v := s.sessions[id]
	if v == nil || v.creator != creator {
		return nil, ErrExpired
	}
	return v, nil
}
func (s *Service) create(ctx context.Context, creator string) (*session, error) {
	s.mu.Lock()
	s.cleanLocked()
	if len(s.sessions) >= capacity {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	vctx, cancel := context.WithTimeout(s.ctx, lifetime)
	v := &session{id: token(), creator: creator, expires: time.Now().Add(lifetime), ctx: vctx, cancel: cancel, known: map[string]int{}, status: "pending", changed: make(chan struct{}, 1)}
	s.sessions[v.id] = v
	s.mu.Unlock()
	// 首批只等待本地读取，平台请求不阻塞页面。
	items, err := s.reader.CreatorVideos(ctx, s.platform, creator, "", pageSize)
	if err != nil {
		v.cancel()
		s.mu.Lock()
		delete(s.sessions, v.id)
		s.mu.Unlock()
		return nil, err
	}
	v.mu.Lock()
	for _, item := range items {
		v.merge(item.CreatorVideo)
	}
	if len(items) > 0 {
		v.localAfter = items[len(items)-1].ID
	}
	v.localDone = len(items) < pageSize
	v.mu.Unlock()
	s.kick(v)
	return v, nil
}
func (v *session) merge(item source.CreatorVideo) {
	if item.ID == "" {
		return
	}
	if index, ok := v.known[item.ID]; ok {
		if v.items[index].CoverURL == "" {
			v.items[index].CoverURL = item.CoverURL
		}
		return
	}
	v.known[item.ID] = len(v.items)
	v.items = append(v.items, item)
}
func (v *session) signal() {
	select {
	case v.changed <- struct{}{}:
	default:
	}
}
func (s *Service) kick(v *session) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.busy || v.status != "pending" {
		return
	}
	v.busy = true
	go s.advance(v)
}
func (s *Service) advance(v *session) {
	defer func() { v.mu.Lock(); v.busy = false; v.mu.Unlock(); v.signal() }()
	v.mu.Lock()
	target := len(v.items) + pageSize
	v.mu.Unlock()
	for work := 0; work < 8; work++ {
		v.mu.Lock()
		if v.ctx.Err() != nil || v.steps >= maxSteps || len(v.items) >= maxVideos {
			v.status = "partial"
			v.issue = "discovery_limit"
			v.mu.Unlock()
			return
		}
		if v.localDone && v.remoteDone {
			v.status = "complete"
			v.mu.Unlock()
			return
		}
		local := !v.localDone && (work > 0 || v.remoteDone)
		after, cursor := v.localAfter, v.remoteCursor
		v.steps++
		v.mu.Unlock()
		if local {
			items, err := s.reader.CreatorVideos(v.ctx, s.platform, v.creator, after, pageSize)
			v.mu.Lock()
			if err != nil {
				v.status = "partial"
				v.issue = "storage_failed"
				v.mu.Unlock()
				return
			}
			for _, item := range items {
				v.merge(item.CreatorVideo)
			}
			if len(items) > 0 {
				v.localAfter = items[len(items)-1].ID
			}
			v.localDone = len(items) < pageSize
			v.mu.Unlock()
		} else {
			if s.queries == nil {
				v.mu.Lock()
				v.remoteDone = true
				v.mu.Unlock()
				continue
			}
			page, err := s.queries.GetCreatorVideoPage(v.ctx, s.platform, v.creator, cursor)
			v.mu.Lock()
			if err != nil {
				var se *source.Error
				if errors.As(err, &se) && se.Kind == source.UnsupportedOperation {
					v.remoteDone = true
					v.issue = "unsupported_operation"
					v.mu.Unlock()
					continue
				}
				v.status = "partial"
				v.issue = "query_failed"
				if errors.As(err, &se) {
					v.issue = string(se.Kind)
					v.retryAt = se.RetryAt
				}
				v.mu.Unlock()
				return
			}
			if len(page.Items) > 200 || (page.NextCursor != "" && page.NextCursor == cursor) {
				v.status = "partial"
				v.issue = "invalid_response"
				v.mu.Unlock()
				return
			}
			for _, item := range page.Items {
				v.merge(item)
			}
			v.remoteCursor = page.NextCursor
			v.remoteDone = page.NextCursor == ""
			v.mu.Unlock()
		}
		v.signal()
		v.mu.Lock()
		done := v.localDone && v.remoteDone
		if done {
			v.status = "complete"
		}
		enough := len(v.items) >= target
		v.mu.Unlock()
		if done || enough {
			return
		}
	}
}

// List 统一分页并按当前管理员关注过滤；retry 只由显式重试操作设置。
func (s *Service) List(ctx context.Context, creator, cursor string, limit int, retry bool) (Page, error) {
	if !valid(creator) || limit < 1 || limit > 100 {
		return Page{}, ErrInput
	}
	var v *session
	var err error
	offset := 0
	if cursor == "" {
		v, err = s.create(ctx, creator)
	} else {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 {
			return Page{}, ErrInput
		}
		offset, err = strconv.Atoi(parts[1])
		if err != nil || offset < 0 {
			return Page{}, ErrInput
		}
		v, err = s.find(parts[0], creator)
	}
	if err != nil {
		return Page{}, err
	}
	v.mu.Lock()
	if offset > len(v.items) {
		v.mu.Unlock()
		return Page{}, ErrInput
	}
	if retry && v.status == "partial" && !v.busy && !time.Now().Before(v.retryAt) && v.ctx.Err() == nil && v.steps < maxSteps && len(v.items) < maxVideos {
		v.status = "pending"
		v.issue = ""
		v.retryAt = time.Time{}
	}
	end := offset + 200
	if end > len(v.items) {
		end = len(v.items)
	}
	batch := append([]source.CreatorVideo(nil), v.items[offset:end]...)
	v.mu.Unlock()
	ids := make([]string, len(batch))
	for i, item := range batch {
		ids[i] = item.ID
	}
	states, err := s.reader.VideoStates(ctx, s.platform, ids)
	if err != nil {
		return Page{}, err
	}
	result := Page{Items: []storage.CreatorVideoRecord{}, Session: v.id}
	for _, item := range batch {
		if len(result.Items) >= limit {
			break
		}
		offset++
		state := states[item.ID]
		if state.Subscribed {
			continue
		}
		if !state.Recorded {
			state.CreatorVideo = item
		} else if state.CoverURL == "" {
			state.CoverURL = item.CoverURL
		}
		result.Items = append(result.Items, state)
	}
	v.mu.Lock()
	result.State = v.status
	result.Issue = v.issue
	result.RetryAt = v.retryAt
	more := offset < len(v.items) || v.status != "complete"
	v.mu.Unlock()
	if more {
		result.NextCursor = v.id + ":" + strconv.Itoa(offset)
	}
	if offset == end {
		s.kick(v)
	}
	return result, nil
}

// Recorded 只读本地并保留已跟踪项，使用稳定的最后一个视频 ID 续页。
func (s *Service) Recorded(ctx context.Context, creator, cursor string, limit int) (Page, error) {
	if !valid(creator) || limit < 1 || limit > 100 {
		return Page{}, ErrInput
	}
	items, err := s.reader.CreatorVideos(ctx, s.platform, creator, cursor, limit)
	if err != nil {
		return Page{}, err
	}
	result := Page{Items: items, State: "complete"}
	if len(items) == limit {
		result.NextCursor = items[len(items)-1].ID
	}
	return result, nil
}

// CancelSession 停止当前发现并保留成功结果至过期，不回滚已经保存的内容。
func (s *Service) CancelSession(id, creator string) error {
	v, err := s.find(id, creator)
	if err != nil {
		return err
	}
	v.cancel()
	v.mu.Lock()
	v.status = "partial"
	v.issue = "cancelled"
	v.mu.Unlock()
	v.signal()
	return nil
}
