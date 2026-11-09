// jobs.go 执行显式选择或作者全部待添加视频的导入，保留逐项结果并支持取消及恢复。
// 任务只枚举本次有界发现范围；设计见 docs/video-discovery.md。
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// ImportRequest 的 All 与 Videos 二选一；All 绑定同一作者的有效发现会话。
type ImportRequest struct {
	Creator         string   `json:"creator_id"`
	Videos          []string `json:"videos"`
	All             bool     `json:"all"`
	Session         string   `json:"session"`
	IntervalSeconds int64    `json:"interval_seconds"`
}
type ImportResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Progress 的 complete 表示发现结束且全部导入成功，拒绝或失败均报告 partial。
type Progress struct {
	ID       string         `json:"id"`
	Creator  string         `json:"creator_id"`
	State    string         `json:"state"`
	Added    int            `json:"added"`
	Existing int            `json:"existing"`
	Rejected int            `json:"rejected"`
	Failed   int            `json:"failed"`
	Issue    string         `json:"issue,omitempty"`
	Results  []ImportResult `json:"results"`
}
type job struct {
	mu           sync.Mutex
	id           string
	request      ImportRequest
	ctx          context.Context
	cancel       context.CancelFunc
	expires      time.Time
	state, issue string
	results      map[string]ImportResult
	position     int
	busy         bool
}

// Start 幂等创建导入任务；相同范围重复提交复用原任务，失败重试须显式 Retry。
func (s *Service) Start(request ImportRequest) (Progress, error) {
	if !valid(request.Creator) || (request.All && len(request.Videos) > 0) || (!request.All && (len(request.Videos) == 0 || len(request.Videos) > 100)) {
		return Progress{}, ErrInput
	}
	if request.IntervalSeconds == 0 {
		request.IntervalSeconds = 10800
	}
	if request.IntervalSeconds < 360 || request.IntervalSeconds > 2592000 {
		return Progress{}, ErrInput
	}
	if request.All {
		if _, err := s.find(request.Session, request.Creator); err != nil {
			return Progress{}, err
		}
	} else {
		seen := map[string]bool{}
		ids := []string{}
		for _, id := range request.Videos {
			if !valid(id) {
				return Progress{}, ErrInput
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		request.Videos = ids
		request.Session = ""
	}
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(encoded)
	key := hex.EncodeToString(digest[:])
	s.mu.Lock()
	s.cleanLocked()
	if id := s.keys[key]; id != "" {
		v := s.jobs[id]
		s.mu.Unlock()
		return v.snapshot(), nil
	}
	if len(s.jobs) >= capacity {
		s.mu.Unlock()
		return Progress{}, ErrCapacity
	}
	ctx, cancel := context.WithTimeout(s.ctx, lifetime)
	v := &job{id: token(), request: request, ctx: ctx, cancel: cancel, expires: time.Now().Add(lifetime), state: "running", results: map[string]ImportResult{}, busy: true}
	s.jobs[v.id] = v
	s.keys[key] = v.id
	s.mu.Unlock()
	go s.run(v, nil)
	return v.snapshot(), nil
}
func (v *job) snapshot() Progress {
	v.mu.Lock()
	defer v.mu.Unlock()
	result := Progress{ID: v.id, Creator: v.request.Creator, State: v.state, Issue: v.issue, Results: []ImportResult{}}
	for _, item := range v.results {
		result.Results = append(result.Results, item)
		switch item.Status {
		case "added":
			result.Added++
		case "existing":
			result.Existing++
		case "rejected":
			result.Rejected++
		case "failed":
			result.Failed++
		}
	}
	sort.Slice(result.Results, func(i, j int) bool { return result.Results[i].ID < result.Results[j].ID })
	return result
}
func (s *Service) getJob(id string) (*job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanLocked()
	v := s.jobs[id]
	if v == nil {
		return nil, ErrExpired
	}
	return v, nil
}
func (s *Service) Progress(id string) (Progress, error) {
	v, err := s.getJob(id)
	if err != nil {
		return Progress{}, err
	}
	return v.snapshot(), nil
}

// Retry 只重试失败项及未完成发现，保留成功和归属拒绝项，不自动循环重试。
func (s *Service) Retry(id string) (Progress, error) {
	v, err := s.getJob(id)
	if err != nil {
		return Progress{}, err
	}
	v.mu.Lock()
	if v.busy {
		v.mu.Unlock()
		return v.snapshot(), nil
	}
	if v.ctx.Err() != nil {
		v.mu.Unlock()
		return Progress{}, ErrExpired
	}
	var ids []string
	for _, item := range v.results {
		if item.Status == "failed" {
			ids = append(ids, item.ID)
		}
	}
	v.state = "running"
	v.issue = ""
	v.busy = true
	v.mu.Unlock()
	if v.request.All {
		session, err := s.find(v.request.Session, v.request.Creator)
		if err != nil {
			v.mu.Lock()
			v.state = "partial"
			v.busy = false
			v.mu.Unlock()
			return Progress{}, err
		}
		session.mu.Lock()
		if session.status == "partial" && time.Now().After(session.retryAt) && session.ctx.Err() == nil && session.steps < maxSteps && len(session.items) < maxVideos {
			session.status = "pending"
			session.issue = ""
		}
		session.mu.Unlock()
		s.kick(session)
	}
	go s.run(v, ids)
	return v.snapshot(), nil
}
func (s *Service) CancelJob(id string) (Progress, error) {
	v, err := s.getJob(id)
	if err != nil {
		return Progress{}, err
	}
	v.cancel()
	v.mu.Lock()
	v.state = "cancelled"
	v.mu.Unlock()
	return v.snapshot(), nil
}
func (s *Service) run(v *job, retry []string) {
	defer func() {
		v.mu.Lock()
		v.busy = false
		if v.state == "running" {
			v.state = "partial"
			// Keep specific reasons such as discovery_incomplete or
			// session_expired; only fall back to cancelled when none was set.
			if v.issue == "" {
				v.issue = "cancelled"
			}
		}
		v.mu.Unlock()
	}()
	importOne := func(id string) bool {
		if v.ctx.Err() != nil || s.queries == nil {
			return false
		}
		// 多个导入任务也串行请求，等待队列可以取消，不持有服务或会话锁。
		select {
		case s.importGate <- struct{}{}:
		case <-v.ctx.Done():
			return false
		}
		status, err := s.queries.IngestVideo(v.ctx, v.request.Creator, id, time.Duration(v.request.IntervalSeconds)*time.Second)
		<-s.importGate
		result := ImportResult{ID: id, Status: status}
		if err != nil {
			result.Status = "failed"
			result.Reason = "query_or_save_failed"
		}
		if status == "rejected" {
			result.Reason = "creator_mismatch"
		}
		v.mu.Lock()
		v.results[id] = result
		v.mu.Unlock()
		return true
	}
	for _, id := range retry {
		if !importOne(id) {
			return
		}
	}
	var session *session
	if v.request.All {
		var err error
		session, err = s.find(v.request.Session, v.request.Creator)
		if err != nil {
			v.mu.Lock()
			v.issue = "session_expired"
			v.mu.Unlock()
			return
		}
	}
	for v.ctx.Err() == nil {
		var id string
		finished := false
		v.mu.Lock()
		index := v.position
		v.mu.Unlock()
		if session == nil {
			if index >= len(v.request.Videos) {
				finished = true
			} else {
				id = v.request.Videos[index]
			}
		} else {
			session.mu.Lock()
			if index < len(session.items) {
				id = session.items[index].ID
			} else if session.status == "complete" {
				finished = true
			} else if session.status == "partial" {
				v.mu.Lock()
				v.issue = "discovery_incomplete"
				v.mu.Unlock()
				session.mu.Unlock()
				return
			}
			session.mu.Unlock()
			if id == "" && !finished {
				s.kick(session)
				select {
				case <-v.ctx.Done():
					return
				case <-session.changed:
				case <-time.After(100 * time.Millisecond):
				}
				continue
			}
		}
		if finished {
			progress := v.snapshot()
			v.mu.Lock()
			if progress.Failed > 0 || progress.Rejected > 0 {
				v.state = "partial"
			} else {
				v.state = "complete"
			}
			v.mu.Unlock()
			return
		}
		if !importOne(id) {
			return
		}
		v.mu.Lock()
		v.position++
		v.mu.Unlock()
	}
}
