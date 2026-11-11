// service_test.go 验证全量多页、分页异常、取消和重启恢复，模拟上游不使用真实 Cookie。
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

type memoryStore struct {
	cookie string
	scan   []byte
	fail   bool
}

func (m *memoryStore) LoadCookie() (string, error) { return m.cookie, nil }
func (m *memoryStore) SaveCookie(value string) error {
	if m.fail {
		return errors.New("disk")
	}
	m.cookie = value
	return nil
}
func (m *memoryStore) PersistentCookie() bool { return true }
func (m *memoryStore) LoadScan(target any) error {
	if len(m.scan) == 0 {
		return nil
	}
	return json.Unmarshal(m.scan, target)
}
func (m *memoryStore) SaveScan(value any) error {
	if m.fail {
		return errors.New("disk")
	}
	data, err := json.Marshal(value)
	m.scan = data
	return err
}

type fakeAPI struct {
	login func(context.Context, string) (bilibili.Account, bilibili.Keys, error)
	page  func(context.Context, int) (bilibili.Page, error)
}

func (f *fakeAPI) Login(ctx context.Context, cookie string) (bilibili.Account, bilibili.Keys, error) {
	if f.login != nil {
		return f.login(ctx, cookie)
	}
	return bilibili.Account{ID: "8", Name: "测试账号"}, bilibili.Keys{}, nil
}
func (f *fakeAPI) Videos(ctx context.Context, _, _ string, page int, _ bilibili.Keys) (bilibili.Page, error) {
	return f.page(ctx, page)
}
func page(total, pn int) bilibili.Page {
	result := bilibili.Page{Total: total, Videos: []bilibili.Video{}}
	for i := (pn - 1) * 30; i < min(pn*30, total); i++ {
		result.Videos = append(result.Videos, bilibili.Video{ID: fmt.Sprintf("BVtest%06d", i), Title: fmt.Sprintf("视频 %d", i)})
	}
	return result
}
func terminal(t *testing.T, s *Service) Result {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r := s.Result(0, 10000)
		if r.Job != nil && r.Job.State != "running" && r.Job.State != "cancelling" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return Result{}
}
func create(t *testing.T, api *fakeAPI, store *memoryStore) *Service {
	t.Helper()
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestScansBeyond120WithoutTruncation(t *testing.T) {
	var pages atomic.Int32
	s := create(t, &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) { pages.Add(1); return page(151, n), nil }}, &memoryStore{cookie: "SESSDATA=fake"})
	if err := s.Start("123", false); err != nil {
		t.Fatal(err)
	}
	r := terminal(t, s)
	if r.Job.State != "complete" || r.Job.Count != 151 || pages.Load() != 6 {
		t.Fatal(r.Job, pages.Load())
	}
	r = s.Result(100, 50)
	if r.Offset != 100 || len(r.Videos) != 50 || !r.HasMore {
		t.Fatal(r)
	}
}
func TestRepeatedPageStopsAndRetainsResults(t *testing.T) {
	var calls atomic.Int32
	s := create(t, &fakeAPI{page: func(context.Context, int) (bilibili.Page, error) { calls.Add(1); return page(90, 1), nil }}, &memoryStore{cookie: "SESSDATA=fake"})
	_ = s.Start("123", false)
	r := terminal(t, s)
	if r.Job.State != "partial" || r.Job.ErrorCode != "pagination_stalled" || r.Job.Count != 30 || calls.Load() != 2 {
		t.Fatal(r.Job, calls.Load())
	}
}
func TestPartialResumeDoesNotRepeatSuccessfulPages(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var mu sync.Mutex
	calls := map[int]int{}
	api := &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) {
		mu.Lock()
		calls[n]++
		mu.Unlock()
		if n == 2 && fail.Load() {
			return bilibili.Page{}, &bilibili.Error{Code: "restricted", Message: "需验证"}
		}
		return page(61, n), nil
	}}
	s := create(t, api, &memoryStore{cookie: "SESSDATA=fake"})
	_ = s.Start("123", false)
	first := terminal(t, s)
	if first.Job.State != "partial" || first.Job.Count != 30 {
		t.Fatal(first.Job)
	}
	fail.Store(false)
	if err := s.Start("123", true); err != nil {
		t.Fatal(err)
	}
	r := terminal(t, s)
	mu.Lock()
	firstPageCalls := calls[1]
	mu.Unlock()
	if r.Job.State != "complete" || r.Job.Count != 61 || r.Job.ID != first.Job.ID || firstPageCalls != 1 {
		t.Fatal(r.Job, firstPageCalls)
	}
}
func TestPrematureEmptyDoesNotAdvanceCheckpoint(t *testing.T) {
	s := create(t, &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) {
		if n == 2 {
			return bilibili.Page{Total: 90}, nil
		}
		return page(90, n), nil
	}}, &memoryStore{cookie: "SESSDATA=fake"})
	_ = s.Start("123", false)
	r := terminal(t, s)
	if r.Job.State != "partial" || r.Job.Count != 30 || r.Job.Pages != 1 {
		t.Fatal(r.Job)
	}
	s.mu.Lock()
	next := s.job.NextPage
	s.mu.Unlock()
	if next != 2 {
		t.Fatal("empty page advanced checkpoint", next)
	}
}
func TestRestartRestoresCheckpointAndCookieRemainsSeparate(t *testing.T) {
	started := make(chan struct{})
	store := &memoryStore{cookie: "SESSDATA=fake-private"}
	s := create(t, &fakeAPI{page: func(ctx context.Context, n int) (bilibili.Page, error) {
		if n == 2 {
			close(started)
			<-ctx.Done()
			return bilibili.Page{}, ctx.Err()
		}
		return page(61, n), nil
	}}, store)
	_ = s.Start("123", false)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second page not started")
	}
	if _, err := s.Configure(context.Background(), "SESSDATA=other"); err == nil {
		t.Fatal("changed account while scanning")
	}
	s.Close()
	var persisted Job
	if err := json.Unmarshal(store.scan, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.State != "interrupted" || persisted.NextPage != 2 || len(persisted.Videos) != 30 {
		t.Fatal(persisted)
	}
	if string(store.scan) == "" || json.Valid(store.scan) == false {
		t.Fatal("state not saved")
	}
	restored := create(t, &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) { return page(61, n), nil }}, store)
	if err := restored.Start("123", true); err != nil {
		t.Fatal(err)
	}
	r := terminal(t, restored)
	if r.Job.State != "complete" || r.Job.Count != 61 {
		t.Fatal(r.Job)
	}
}
func TestChangedAccountCannotResumeOldVisibility(t *testing.T) {
	store := &memoryStore{cookie: "SESSDATA=fake"}
	api := &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) {
		if n == 2 {
			return bilibili.Page{}, &bilibili.Error{Code: "restricted", Message: "验证"}
		}
		return page(61, n), nil
	}}
	s := create(t, api, store)
	_ = s.Start("123", false)
	_ = terminal(t, s)
	api.login = func(context.Context, string) (bilibili.Account, bilibili.Keys, error) {
		return bilibili.Account{ID: "9", Name: "其他账号"}, bilibili.Keys{}, nil
	}
	_ = s.Start("123", true)
	r := terminal(t, s)
	if r.Job.ErrorCode != "account_changed" || r.Job.Count != 30 {
		t.Fatal(r.Job)
	}
}
func TestCancelRetainsSuccessfulPage(t *testing.T) {
	started := make(chan struct{})
	s := create(t, &fakeAPI{page: func(ctx context.Context, n int) (bilibili.Page, error) {
		if n == 2 {
			close(started)
			<-ctx.Done()
			return bilibili.Page{}, ctx.Err()
		}
		return page(61, n), nil
	}}, &memoryStore{cookie: "SESSDATA=fake"})
	_ = s.Start("123", false)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not start")
	}
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	r := terminal(t, s)
	if r.Job.State != "cancelled" || r.Job.Count != 30 || !r.Job.CanResume {
		t.Fatal(r.Job)
	}
}
func TestCountChangeCannotReportComplete(t *testing.T) {
	s := create(t, &fakeAPI{page: func(_ context.Context, n int) (bilibili.Page, error) {
		if n == 1 {
			return page(60, n), nil
		}
		return page(59, n), nil
	}}, &memoryStore{cookie: "SESSDATA=fake"})
	_ = s.Start("123", false)
	r := terminal(t, s)
	if r.Job.State != "partial" || r.Job.Count != 59 || r.Job.ErrorCode != "incomplete" {
		t.Fatal(r.Job)
	}
}
func TestFailedCredentialSaveDoesNotReplaceOriginal(t *testing.T) {
	store := &memoryStore{cookie: "SESSDATA=old", fail: true}
	s := create(t, &fakeAPI{}, store)
	if _, err := s.Configure(context.Background(), "SESSDATA=new"); err == nil {
		t.Fatal("reported successful credential persistence")
	}
	if store.cookie != "SESSDATA=old" || s.cookie != store.cookie {
		t.Fatal("old cookie was overwritten")
	}
}
