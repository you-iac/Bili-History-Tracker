// Package scanner 管理本地 Cookie 配置和单个可恢复的 UP 主视频 ID 扫描任务。
// 平台请求在互斥锁外执行，任务不绑定网页请求；每页结果与续页位置一起落盘。
// 只枚举当前可见投稿，不创建服务器跟踪、自动规则或下载。见 docs/client-discovery.md。
package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

type Platform interface {
	Login(context.Context, string) (bilibili.Account, bilibili.Keys, error)
	Videos(context.Context, string, string, int, bilibili.Keys) (bilibili.Page, error)
}
type Storage interface {
	LoadCookie() (string, error)
	SaveCookie(string) error
	PersistentCookie() bool
	LoadScan(any) error
	SaveScan(any) error
}

// Config 只返回账号摘要和存储方式；不提供读取 Cookie 的接口。
type Config struct {
	Configured bool              `json:"configured"`
	Persistent bool              `json:"persistent"`
	Account    *bilibili.Account `json:"account"`
}
type Job struct {
	Version      int              `json:"version"`
	ID           string           `json:"id"`
	CreatorID    string           `json:"creator_id"`
	AccountID    string           `json:"account_id,omitempty"`
	State        string           `json:"state"`
	Message      string           `json:"message"`
	ErrorCode    string           `json:"error_code,omitempty"`
	Total        *int             `json:"total"`
	Pages        int              `json:"pages"`
	NextPage     int              `json:"next_page"`
	CountChanged bool             `json:"count_changed"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Videos       []bilibili.Video `json:"videos"`
}
type Summary struct {
	ID        string    `json:"id"`
	CreatorID string    `json:"creator_id"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	ErrorCode string    `json:"error_code,omitempty"`
	Total     *int      `json:"total"`
	Count     int       `json:"count"`
	Pages     int       `json:"pages"`
	CanResume bool      `json:"can_resume"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Result struct {
	Job     *Summary         `json:"job"`
	Videos  []bilibili.Video `json:"videos"`
	Offset  int              `json:"offset"`
	HasMore bool             `json:"has_more"`
}

type Service struct {
	mu          sync.Mutex
	api         Platform
	store       Storage
	cookie      string
	account     *bilibili.Account
	configuring bool
	closed      bool
	job         *Job
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	delay       time.Duration
}

func New(api Platform, store Storage, pageDelay time.Duration) (*Service, error) {
	cookie, err := store.LoadCookie()
	if err != nil {
		return nil, err
	}
	if cookie != "" {
		if _, err := bilibili.NormalizeCookie(cookie); err != nil {
			return nil, errors.New("已保存 Cookie 无效，请清除 cookie.protected 后重新配置")
		}
	}
	s := &Service{api: api, store: store, cookie: cookie, delay: pageDelay}
	if err := store.LoadScan(&s.job); err != nil {
		return nil, errors.New("扫描记录无法读取，请备份并移除 scan.json 后重试")
	}
	if s.job != nil {
		j := s.job
		uid, err := bilibili.CreatorID(j.CreatorID)
		if err != nil || uid != j.CreatorID || j.Version != 1 || j.ID == "" || j.NextPage < 1 || j.NextPage != j.Pages+1 || j.Total != nil && *j.Total < 0 {
			return nil, errors.New("扫描记录格式无效，请备份 scan.json 后重新扫描")
		}
		switch j.State {
		case "running", "cancelling":
			j.State = "interrupted"
			j.Message = "Agent 已重新启动，可继续上次扫描。"
			j.UpdatedAt = time.Now().UTC()
			if err := store.SaveScan(j); err != nil {
				return nil, err
			}
		case "complete", "partial", "cancelled", "interrupted":
		default:
			return nil, errors.New("扫描记录状态无效")
		}
	}
	return s, nil
}

func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Config{Configured: s.cookie != "", Persistent: s.store.PersistentCookie()}
	if s.account != nil {
		a := *s.account
		c.Account = &a
	}
	return c
}

func conflict() error {
	return &bilibili.Error{Code: "conflict", Message: "请等待当前操作结束，或先停止扫描。"}
}
func (s *Service) busy() bool {
	return s.job != nil && (s.job.State == "running" || s.job.State == "cancelling")
}

// Configure 先验证后替换；验证失败不覆盖原凭据，扫描中不允许变更账号。
func (s *Service) Configure(ctx context.Context, input string) (bilibili.Account, error) {
	cookie, err := bilibili.NormalizeCookie(input)
	if err != nil {
		return bilibili.Account{}, err
	}
	return s.configure(ctx, cookie, true)
}
func (s *Service) Validate(ctx context.Context) (bilibili.Account, error) {
	s.mu.Lock()
	cookie := s.cookie
	s.mu.Unlock()
	if cookie == "" {
		return bilibili.Account{}, &bilibili.Error{Code: "login_required", Message: "请先配置 Cookie。"}
	}
	return s.configure(ctx, cookie, false)
}
func (s *Service) configure(ctx context.Context, cookie string, replace bool) (bilibili.Account, error) {
	s.mu.Lock()
	if s.closed || s.busy() || s.configuring || (!replace && s.cookie != cookie) {
		s.mu.Unlock()
		return bilibili.Account{}, conflict()
	}
	s.configuring = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.configuring = false; s.mu.Unlock() }()
	a, _, err := s.api.Login(ctx, cookie)
	if err != nil {
		if !replace {
			s.mu.Lock()
			s.account = nil
			s.mu.Unlock()
		}
		return bilibili.Account{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return bilibili.Account{}, conflict()
	}
	if replace {
		if err := s.store.SaveCookie(cookie); err != nil {
			return bilibili.Account{}, &bilibili.Error{Code: "storage", Message: "Cookie 保护或保存失败，原配置未变更。"}
		}
		s.cookie = cookie
	}
	s.account = &a
	return a, nil
}
func (s *Service) ClearCookie() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.busy() || s.configuring {
		return conflict()
	}
	if err := s.store.SaveCookie(""); err != nil {
		return &bilibili.Error{Code: "storage", Message: "清除本地凭据失败。"}
	}
	s.cookie = ""
	s.account = nil
	return nil
}

// Start 显式开始新任务或恢复当前任务；仅保留最近一次扫描，成功页逐页保存。
func (s *Service) Start(input string, resume bool) error {
	uid, err := bilibili.CreatorID(input)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.busy() || s.configuring {
		return conflict()
	}
	if s.cookie == "" {
		return &bilibili.Error{Code: "login_required", Message: "请先配置并验证 B站 Cookie。"}
	}
	var j Job
	if resume {
		if s.job == nil || s.job.CreatorID != uid || s.job.State == "complete" {
			return &bilibili.Error{Code: "invalid_input", Message: "没有可以继续的扫描，请重新开始。"}
		}
		j = *s.job
		j.Videos = append([]bilibili.Video{}, j.Videos...)
	} else {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		j = Job{Version: 1, ID: hex.EncodeToString(id[:]), CreatorID: uid, NextPage: 1, CreatedAt: time.Now().UTC(), Videos: []bilibili.Video{}}
	}
	j.State = "running"
	j.Message = "正在验证登录状态…"
	j.ErrorCode = ""
	j.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveScan(&j); err != nil {
		return &bilibili.Error{Code: "storage", Message: "无法保存扫描任务，请检查数据目录。"}
	}
	s.job = &j
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	cookie := s.cookie
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.run(ctx, &j, cookie) }()
	return nil
}

func (s *Service) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.busy() {
		return &bilibili.Error{Code: "conflict", Message: "当前没有正在运行的扫描。"}
	}
	s.job.State = "cancelling"
	s.job.Message = "正在停止，保留已获取的结果…"
	s.cancel()
	return nil
}

// Close 取消任务并等待结果落盘。不能与 Start 并发新增任务。
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) Result(offset, limit int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Result{Videos: []bilibili.Video{}, Offset: offset}
	if s.job == nil {
		return r
	}
	j := s.job
	var total *int
	if j.Total != nil {
		v := *j.Total
		total = &v
	}
	r.Job = &Summary{ID: j.ID, CreatorID: j.CreatorID, State: j.State, Message: j.Message, ErrorCode: j.ErrorCode, Total: total, Count: len(j.Videos), Pages: j.Pages, CanResume: j.State != "complete" && j.State != "running" && j.State != "cancelling", UpdatedAt: j.UpdatedAt}
	offset = max(0, min(offset, len(j.Videos)))
	end := min(len(j.Videos), offset+max(0, limit))
	r.Offset = offset
	r.Videos = append(r.Videos, j.Videos[offset:end]...)
	r.HasMore = end < len(j.Videos)
	return r
}

func (s *Service) run(ctx context.Context, j *Job, cookie string) {
	a, keys, err := s.api.Login(ctx, cookie)
	if err != nil {
		s.finish(j, ctx, "partial", err)
		return
	}
	s.mu.Lock()
	if j.AccountID != "" && j.AccountID != a.ID {
		s.mu.Unlock()
		s.finish(j, ctx, "partial", &bilibili.Error{Code: "account_changed", Message: "上次扫描使用了另一个账号，请切回原账号继续，或重新开始扫描。"})
		return
	}
	j.AccountID = a.ID
	s.account = &a
	seen := make(map[string]bool, len(j.Videos))
	for _, v := range j.Videos {
		seen[v.ID] = true
	}
	page := j.NextPage
	s.mu.Unlock()
	for {
		if err := wait(ctx, s.delay); err != nil {
			s.finish(j, ctx, "cancelled", err)
			return
		}
		s.mu.Lock()
		j.Message = fmt.Sprintf("正在读取第 %d 页…", page)
		s.mu.Unlock()
		result, err := s.page(ctx, cookie, j.CreatorID, page, keys)
		if err != nil {
			s.finish(j, ctx, "partial", err)
			return
		}
		if ctx.Err() != nil {
			s.finish(j, ctx, "cancelled", ctx.Err())
			return
		}
		s.mu.Lock()
		if len(result.Videos) == 0 && page < (result.Total+bilibili.PageSize-1)/bilibili.PageSize {
			s.mu.Unlock()
			s.finish(j, ctx, "partial", &bilibili.Error{Code: "incomplete", Message: "平台提前返回空页，数量未覆盖全部投稿；已保留结果，可稍后重试当前页。"})
			return
		}
		var fresh []bilibili.Video
		for _, v := range result.Videos {
			if !seen[v.ID] {
				seen[v.ID] = true
				fresh = append(fresh, v)
			}
		}
		if len(result.Videos) > 0 && len(fresh) == 0 {
			s.mu.Unlock()
			s.finish(j, ctx, "partial", &bilibili.Error{Code: "pagination_stalled", Message: "投稿分页没有返回新视频，已停止并保留结果，可稍后继续。"})
			return
		}
		if j.Total != nil && *j.Total != result.Total {
			j.CountChanged = true
		}
		total := result.Total
		j.Total = &total
		j.Videos = append(j.Videos, fresh...)
		j.Pages = page
		j.NextPage = page + 1
		j.UpdatedAt = time.Now().UTC()
		terminal := page >= (result.Total+bilibili.PageSize-1)/bilibili.PageSize
		unexpectedEmpty := len(result.Videos) == 0 && !terminal
		if err := s.store.SaveScan(j); err != nil {
			s.mu.Unlock()
			s.finish(j, ctx, "partial", &bilibili.Error{Code: "storage", Message: "结果落盘失败，当前页面结果可能未保存，请检查磁盘后重新扫描。"})
			return
		}
		s.mu.Unlock()
		if unexpectedEmpty {
			s.finish(j, ctx, "partial", &bilibili.Error{Code: "incomplete", Message: "平台提前返回空页，数量未覆盖全部投稿；已保留结果，建议重新扫描。"})
			return
		}
		if terminal {
			s.mu.Lock()
			complete := len(j.Videos) == result.Total && !j.CountChanged
			s.mu.Unlock()
			if !complete {
				s.finish(j, ctx, "partial", &bilibili.Error{Code: "incomplete", Message: "分页已结束，但数量有变化或去重后与平台总量不一致，请重新扫描核对。"})
			} else {
				s.finish(j, ctx, "complete", nil)
			}
			return
		}
		page++
	}
}

func (s *Service) page(ctx context.Context, cookie, uid string, page int, keys bilibili.Keys) (bilibili.Page, error) {
	for attempt := 0; ; attempt++ {
		result, err := s.api.Videos(ctx, cookie, uid, page, keys)
		if err == nil {
			return result, nil
		}
		var upstream *bilibili.Error
		if attempt >= 2 || !errors.As(err, &upstream) || (upstream.Code != "network" && upstream.Code != "upstream" && upstream.Code != "rate_limited") {
			return bilibili.Page{}, err
		}
		delay := max(time.Duration(attempt+1)*time.Second, upstream.RetryAfter)
		s.mu.Lock()
		s.job.Message = "平台暂未响应，正在等待后重试当前页…"
		s.mu.Unlock()
		if err := wait(ctx, delay); err != nil {
			return bilibili.Page{}, err
		}
	}
}
func wait(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (s *Service) finish(j *Job, ctx context.Context, state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		state = "cancelled"
		j.Message = "已停止，已获取的视频 ID 已保留。"
		j.ErrorCode = ""
	} else if err != nil {
		var known *bilibili.Error
		if errors.As(err, &known) {
			j.Message = known.Message
			j.ErrorCode = known.Code
			if known.Code == "login_required" {
				s.account = nil
			}
		} else {
			j.Message = "扫描未完成，已保留结果，可稍后继续。"
			j.ErrorCode = "upstream"
		}
	} else {
		j.Message = "当前账号可见投稿已枚举完成。"
		j.ErrorCode = ""
	}
	if s.closed {
		state = "interrupted"
		j.Message = "Agent 已停止，下次启动后可继续扫描。"
	}
	j.State = state
	j.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveScan(j); err != nil {
		j.State = "partial"
		j.Message = "扫描状态保存失败，部分结果可能未落盘，请检查数据目录。"
		j.ErrorCode = "storage"
	}
	s.cancel = nil
}
