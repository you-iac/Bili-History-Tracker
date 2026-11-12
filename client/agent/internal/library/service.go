// Package library 管理本地私人 UP/视频关注、服务器地址与最近读取的公共资料。
// 写入成功才更新内存；网络操作在锁外执行，取消关注不会请求服务器停采。见 docs/client-library.md。
package library

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
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

func (e *Error) Error() string { return e.Message }

type Storage interface {
	LoadLibrary(any) error
	SaveLibrary(any) error
}
type Follow struct {
	Kind      string         `json:"kind"`
	ID        string         `json:"id"`
	AddedAt   time.Time      `json:"added_at"`
	Status    *remote.Status `json:"remote,omitempty"`
	Data      *remote.Detail `json:"data,omitempty"`
	ServerURL string         `json:"server_url,omitempty"`
	UpdatedAt *time.Time     `json:"updated_at,omitempty"`
	LastError string         `json:"last_error,omitempty"`
}
type state struct {
	Version   int      `json:"version"`
	ServerURL string   `json:"server_url"`
	Follows   []Follow `json:"follows"`
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
	mu     sync.Mutex
	store  Storage
	client *remote.Client
	state  state
}

func New(store Storage, client *remote.Client) (*Service, error) {
	s := &Service{store: store, client: client, state: state{Version: 1, Follows: []Follow{}}}
	if err := store.LoadLibrary(&s.state); err != nil {
		return nil, errors.New("关注库无法读取，请先备份 library.json 后检查文件")
	}
	base, err := remote.NormalizeBase(s.state.ServerURL)
	if err != nil || base != s.state.ServerURL || s.state.Version != 1 || len(s.state.Follows) > 10000 {
		return nil, errors.New("本地关注库格式无效，请备份 library.json 后检查")
	}
	seen := map[string]bool{}
	for _, f := range s.state.Follows {
		id, e := Normalize(f.Kind, f.ID)
		key := f.Kind + ":" + f.ID
		if e != nil || id != f.ID || seen[key] || f.AddedAt.IsZero() {
			return nil, errors.New("本地关注标识或格式无效")
		}
		seen[key] = true
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
	for i := range next.Follows {
		next.Follows[i].Status = nil
		next.Follows[i].Data = nil
		next.Follows[i].ServerURL = ""
		next.Follows[i].UpdatedAt = nil
		next.Follows[i].LastError = ""
	}
	return s.save(next)
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
	f := Follow{Kind: kind, ID: id, AddedAt: time.Now().UTC()}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Page{Items: []Follow{}, Page: max(1, page), Limit: 20}
	q = strings.ToLower(strings.TrimSpace(q))
	items := []Follow{}
	for _, f := range s.state.Follows {
		if f.Kind == "creator" {
			result.Creators++
		} else {
			result.Videos++
		}
		name := ""
		if f.Data != nil {
			name = f.Data.Item.Name
		}
		if (kind == "" || f.Kind == kind) && (q == "" || strings.Contains(strings.ToLower(f.ID+" "+name), q)) {
			items = append(items, f)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AddedAt.After(items[j].AddedAt) })
	result.Total = len(items)
	result.Page = min(result.Page, max(1, (len(items)+19)/20))
	start := (result.Page - 1) * 20
	result.Items = append(result.Items, items[start:min(start+20, len(items))]...)
	return result
}
func (s *Service) find(kind, id string) (Follow, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.state.Follows {
		if f.Kind == kind && f.ID == id {
			return f, s.state.ServerURL, nil
		}
	}
	return Follow{}, "", &Error{Message: "本地尚未关注此目标。", Status: 404}
}
func (s *Service) update(original Follow, base string, change func(*Follow)) (Follow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if base != s.state.ServerURL {
		return Follow{}, &Error{Message: "服务器配置已更改，请重新刷新。", Status: 409}
	}
	next := s.copyState()
	for i, f := range next.Follows {
		if f.Kind == original.Kind && f.ID == original.ID && f.AddedAt.Equal(original.AddedAt) {
			change(&next.Follows[i])
			err := s.save(next)
			return next.Follows[i], err
		}
	}
	return Follow{}, &Error{Message: "该项已在本地取消关注。", Status: 404}
}
func (s *Service) Refresh(ctx context.Context, kind, id string) (Follow, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	f, base, err := s.find(kind, id)
	if err != nil {
		return f, err
	}
	status, err := s.client.Status(ctx, base, kind, id)
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
		v.Data = detail
		v.ServerURL = base
		v.UpdatedAt = &now
		v.LastError = ""
	})
}

// Submit 仅提交已保存的本地关注；远端失败不回滚个人关注。
func (s *Service) Submit(ctx context.Context, kind, id string) (Follow, error) {
	f, base, err := s.find(kind, id)
	if err != nil {
		return f, err
	}
	status, err := s.client.Submit(ctx, base, kind, id)
	if err != nil {
		_, _ = s.update(f, base, func(v *Follow) { v.LastError = err.Error() })
		return f, err
	}
	return s.update(f, base, func(v *Follow) {
		now := time.Now().UTC()
		v.Status = &status
		v.ServerURL = base
		v.UpdatedAt = &now
		v.LastError = ""
	})
}
func (s *Service) Detail(ctx context.Context, kind, id string, page int, seq int64) (remote.Detail, error) {
	_, base, err := s.find(kind, id)
	if err != nil {
		return remote.Detail{}, err
	}
	return s.client.Detail(ctx, base, kind, id, page, seq)
}
