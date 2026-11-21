// summaries_test.go 覆盖待提交关注的只读摘要同步、详情回填与来源/时间范围隔离。
package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestPendingFollowsGetSummariesWithoutSubmittingTracking(t *testing.T) {
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pages := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/creators" || r.Header.Get("Cookie") != "" {
			t.Error("summary sync must only read recorded data", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		pages = append(pages, r.URL.Query().Get("id"))
		page := 1
		id, name := "123", "已有资料的 UP"
		if r.URL.Query().Get("id") == "456" {
			page = 1
			id = "456"
			name = "第二页的 UP"
		}
		metric, growth := int64(85000), int64(59)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(remote.Catalog{Items: []remote.Item{{Kind: "creator", ID: id, Name: name, Metric: &metric, Growth: &growth, Seq: 1, Recorded: true, ObservedAt: 100}}, Page: page, Limit: 1, Total: 2})
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_, _ = s.Add("creator", "123")
	_, _ = s.Add("creator", "456")
	_ = s.Configure(upstream.URL)
	count, err := s.SyncSummaries(context.Background(), "creator")
	if err != nil || count != 2 || strings.Join(pages, ",") != "123,456" {
		t.Fatal(count, pages, err)
	}
	for _, f := range s.ListCatalog("creator", "", 1, 0, false, "popular", "").Items {
		if f.Summary == nil || f.Summary.Name == f.ID || f.Summary.Metric == nil || *f.Summary.Growth != 59 || !f.Pending || f.Data != nil {
			t.Fatal("summary must not depend on tracking or pretend to be a full detail", f)
		}
	}
	reopened, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.ListCatalog("creator", "已有资料", 1, 0, false, "popular", ""); got.Total != 1 || got.Items[0].Summary.Metric == nil {
		t.Fatal("summary lost after restart or name search failed", got)
	}
	_ = reopened.Configure("http://localhost:8182")
	if got := reopened.List("creator", "", 1); got.Items[0].Summary != nil || got.Items[0].SummaryAt != nil {
		t.Fatal("server switch retained summary", got)
	}
}

func TestSummarySyncKeepsPartialResultsAndOldCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("id") == "456" {
			w.WriteHeader(502)
			w.Write([]byte(`{"error":"offline"}`))
			return
		}
		w.Write([]byte(`{"items":[{"kind":"creator","id":"123","name":"新的摘要","observed_at":100}],"page":1,"limit":1,"total":2}`))
	}))
	defer upstream.Close()
	s, _ := New(&failingStore{}, remote.New(nil))
	_ = s.Configure(upstream.URL)
	_, _ = s.Add("creator", "123")
	_, _ = s.Add("creator", "456")
	s.state.Follows[1].Summary = &remote.Item{Kind: "creator", ID: "456", Name: "保留旧资料"}
	s.state.Follows[1].ServerURL = upstream.URL
	count, err := s.SyncSummaries(context.Background(), "creator")
	if err == nil || count != 1 {
		t.Fatal("expected partial sync", count, err)
	}
	got := s.List("creator", "", 1)
	names := map[string]string{}
	for _, f := range got.Items {
		names[f.ID] = f.Summary.Name
	}
	if names["123"] != "新的摘要" || names["456"] != "保留旧资料" {
		t.Fatal(names)
	}
}

func TestSummarySyncRejectsRemovedFollowAndChangedServerGeneration(t *testing.T) {
	for _, change := range []string{"remove", "switch-back"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"items":[{"kind":"creator","id":"123","name":"过期响应"}],"page":1,"limit":18,"total":1}`))
			}))
			defer upstream.Close()
			s, _ := New(&failingStore{}, remote.New(nil))
			_ = s.Configure(upstream.URL)
			_, _ = s.Add("creator", "123")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.SyncSummaries(ctx, "creator"); done <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("read did not start")
			}
			if change == "remove" {
				_ = s.Remove("creator", "123")
			} else {
				_ = s.Configure("http://localhost:8182")
				_ = s.Configure(upstream.URL)
			}
			close(release)
			err := <-done
			if change == "remove" {
				if s.List("creator", "", 1).Total != 0 {
					t.Fatal("removed follow resurrected")
				}
			} else if err == nil || s.List("creator", "", 1).Items[0].Summary != nil {
				t.Fatal("accepted stale source generation", err)
			}
		})
	}
}

func TestDetailUpdatesCardButOnlyCachesDefaultFullRange(t *testing.T) {
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/detail/creator/123") {
			t.Error("read submitted tracking", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		value := int64(30)
		if r.URL.Query().Get("days") == "90" {
			value = 90
		}
		json.NewEncoder(w).Encode(map[string]any{"item": map[string]any{"kind": "creator", "id": "123", "name": "详情中的真实名称", "metric": value, "observed_at": value}, "metrics": map[string]any{"points": []map[string]int64{{"at": 1700000000000, "value": value}}}, "versions": []any{}, "version_page": 1, "version_limit": 10})
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	_, _ = s.Add("creator", "123")
	if _, err = s.Detail(context.Background(), "creator", "123", 1, 0, "30"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Detail(context.Background(), "creator", "123", 1, 0, "90"); err != nil {
		t.Fatal(err)
	}
	f := s.List("creator", "", 1).Items[0]
	if f.Summary == nil || *f.Summary.Metric != 90 || f.Data == nil || f.Data.Metrics.Points[0].Value != 30 || !f.Pending {
		t.Fatal("card was not updated or range cache contaminated", f)
	}
	upstream.Close()
	reopened, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	view, err := reopened.Detail(context.Background(), "creator", "123", 1, 0, "30")
	if err != nil || !view.Cached || view.Metrics.Points[0].Value != 30 {
		t.Fatal("default detail not persisted", view, err)
	}
}

func TestDetailCacheWriteFailureStillReturnsReadData(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"item":{"kind":"creator","id":"123","name":"读取成功"},"metrics":{"points":[]},"versions":[]}`))
	}))
	defer upstream.Close()
	store := &failingStore{}
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	_, _ = s.Add("creator", "123")
	store.fail = true
	view, err := s.Detail(context.Background(), "creator", "123", 1, 0, "30")
	if err != nil || view.Item.Name != "读取成功" || view.CacheError == "" || s.List("creator", "", 1).Items[0].Summary != nil {
		t.Fatal(view, err)
	}
}
