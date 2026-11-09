// service_test.go 验证融合过滤、跨页添加全部、失败恢复和会话边界。
package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

type memoryReader struct {
	mu    sync.Mutex
	items map[string]storage.CreatorVideoRecord
}

func (m *memoryReader) CreatorVideos(_ context.Context, _, _, after string, limit int) ([]storage.CreatorVideoRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []string{}
	for id, item := range m.items {
		if item.Recorded && id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	items := []storage.CreatorVideoRecord{}
	for _, id := range ids {
		items = append(items, m.items[id])
	}
	return items, nil
}
func (m *memoryReader) VideoStates(_ context.Context, _ string, ids []string) (map[string]storage.CreatorVideoRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := map[string]storage.CreatorVideoRecord{}
	for _, id := range ids {
		item := m.items[id]
		item.ID = id
		items[id] = item
	}
	return items, nil
}

type queryStub struct {
	page   func(context.Context, string) (source.CreatorVideoPage, error)
	ingest func(context.Context, string) (string, error)
}

func (q *queryStub) GetCreatorVideoPage(ctx context.Context, _, _, cursor string) (source.CreatorVideoPage, error) {
	return q.page(ctx, cursor)
}
func (q *queryStub) IngestVideo(ctx context.Context, _, id string, _ time.Duration) (string, error) {
	return q.ingest(ctx, id)
}
func record(id string, tracked bool) storage.CreatorVideoRecord {
	return storage.CreatorVideoRecord{CreatorVideo: source.CreatorVideo{ID: id, Title: "local " + id}, Recorded: true, Tracking: tracked, Subscribed: tracked, Versions: 2}
}
func waitJob(t *testing.T, s *Service, id string) Progress {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		p, err := s.Progress(id)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := s.getJob(id)
		v.mu.Lock()
		busy := v.busy
		v.mu.Unlock()
		if p.State != "running" && !busy {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not settle")
	return Progress{}
}

func TestListBlendsFiltersAndContinuesPastDuplicatePages(t *testing.T) {
	m := &memoryReader{items: map[string]storage.CreatorVideoRecord{"a": record("a", false), "b": record("b", true)}}
	q := &queryStub{page: func(_ context.Context, cursor string) (source.CreatorVideoPage, error) {
		switch cursor {
		case "":
			return source.CreatorVideoPage{NextCursor: "one"}, nil
		case "one":
			return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "a", Title: "remote"}, {ID: "b"}}, NextCursor: "two"}, nil
		default:
			return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "c", Title: "remote c"}}}, nil
		}
	}, ingest: func(context.Context, string) (string, error) { t.Error("browsing wrote content"); return "", nil }}
	s := New(m, q, "other-platform")
	defer s.Close()
	cursor := ""
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		page, err := s.List(context.Background(), "opaque-author", cursor, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] || item.Subscribed || item.ID == "b" {
				t.Fatalf("bad item: %+v", item)
			}
			seen[item.ID] = true
			if item.ID == "a" && (!item.Recorded || item.Title != "local a") {
				t.Fatalf("local metadata lost: %+v", item)
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(seen) != 2 || !seen["a"] || !seen["c"] {
		t.Fatalf("fusion result: %v", seen)
	}
	page, err := s.Recorded(context.Background(), "opaque-author", "", 100)
	if err != nil || len(page.Items) != 2 || !page.Items[1].Subscribed {
		t.Fatalf("recorded view filtered tracked item: %+v %v", page, err)
	}
}
func TestAllIncludesDisplayedAndUndiscoveredAndIsIdempotent(t *testing.T) {
	m := &memoryReader{items: map[string]storage.CreatorVideoRecord{}}
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("v%03d", i)
		m.items[id] = record(id, i == 1)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	q := &queryStub{page: func(_ context.Context, cursor string) (source.CreatorVideoPage, error) {
		index := 0
		if cursor != "" {
			index, _ = strconv.Atoi(cursor)
		}
		page := source.CreatorVideoPage{}
		for i := index; i < index+10 && i < 70; i++ {
			page.Items = append(page.Items, source.CreatorVideo{ID: fmt.Sprintf("v%03d", i)})
		}
		if index+10 < 70 {
			page.NextCursor = strconv.Itoa(index + 10)
		}
		return page, nil
	}, ingest: func(_ context.Context, id string) (string, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		item := m.items[id]
		if item.Subscribed {
			return "existing", nil
		}
		mu.Lock()
		calls[id]++
		mu.Unlock()
		item.ID = id
		item.Recorded = true
		item.Subscribed = true
		item.Tracking = true
		m.items[id] = item
		return "added", nil
	}}
	s := New(m, q, "other-platform")
	defer s.Close()
	first, err := s.List(context.Background(), "author", "", 1, false)
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	request := ImportRequest{Creator: "author", All: true, Session: first.Session}
	job, err := s.Start(request)
	if err != nil {
		t.Fatal(err)
	}
	result := waitJob(t, s, job.ID)
	if result.State != "complete" || result.Added != 69 || result.Existing != 1 {
		t.Fatalf("all results: %+v", result)
	}
	mu.Lock()
	for id, count := range calls {
		if count != 1 {
			t.Errorf("%s imported %d times", id, count)
		}
	}
	if calls[first.Items[0].ID] != 1 || calls["v069"] != 1 {
		t.Error("loaded or undiscovered item skipped")
	}
	mu.Unlock()
	again, err := s.Start(request)
	if err != nil || again.ID != job.ID {
		t.Fatalf("idempotency: %+v %v", again, err)
	}
}
func TestPartialDiscoveryAndImportResumeWithoutReimportingSuccess(t *testing.T) {
	m := &memoryReader{items: map[string]storage.CreatorVideoRecord{"a": record("a", false)}}
	var mu sync.Mutex
	blocked := true
	calls := map[string]int{}
	q := &queryStub{page: func(_ context.Context, cursor string) (source.CreatorVideoPage, error) {
		mu.Lock()
		defer mu.Unlock()
		if cursor == "" {
			return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "b"}}, NextCursor: "next"}, nil
		}
		if blocked {
			return source.CreatorVideoPage{}, &source.Error{Kind: source.AccessRestricted}
		}
		return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "c"}}}, nil
	}, ingest: func(_ context.Context, id string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[id]++
		if id == "a" && blocked {
			return "failed", errors.New("temporary save failure")
		}
		return "added", nil
	}}
	s := New(m, q, "test")
	defer s.Close()
	first, err := s.List(context.Background(), "author", "", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(ImportRequest{Creator: "author", All: true, Session: first.Session})
	if err != nil {
		t.Fatal(err)
	}
	partial := waitJob(t, s, job.ID)
	if partial.State != "partial" || partial.Failed != 1 || partial.Issue != "discovery_incomplete" {
		t.Fatalf("partial: %+v", partial)
	}
	mu.Lock()
	blocked = false
	mu.Unlock()
	if _, err := s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, s, job.ID)
	if done.State != "complete" || done.Added != 3 || done.Failed != 0 {
		t.Fatalf("resume: %+v", done)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["a"] != 2 || calls["b"] != 1 || calls["c"] != 1 {
		t.Fatalf("retry repeated success: %v", calls)
	}
}
func TestLocalReturnsWhileUpstreamBlockedAndCancellationStopsQuery(t *testing.T) {
	m := &memoryReader{items: map[string]storage.CreatorVideoRecord{"a": record("a", false)}}
	started, stopped := make(chan struct{}), make(chan struct{})
	q := &queryStub{page: func(ctx context.Context, _ string) (source.CreatorVideoPage, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return source.CreatorVideoPage{}, ctx.Err()
	}}
	s := New(m, q, "test")
	defer s.Close()
	first, err := s.List(context.Background(), "author", "", 30, false)
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("local blocked: %+v %v", first, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream not started")
	}
	if err := s.CancelSession(first.Session, "author"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("query ignored cancellation")
	}
	if _, err := s.List(context.Background(), "wrong", first.Session+":0", 30, false); !errors.Is(err, ErrExpired) {
		t.Fatalf("cross-author cursor accepted: %v", err)
	}
	v, _ := s.find(first.Session, "author")
	v.expires = time.Now().Add(-time.Second)
	if _, err := s.List(context.Background(), "author", first.Session+":0", 30, false); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired cursor accepted: %v", err)
	}
}
