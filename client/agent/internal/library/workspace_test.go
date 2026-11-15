// workspace_test.go 验证自动关注、公开搜索隐私、本地总览缓存与后台提交重试。
package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestAutomaticFollowPublicSearchAndOverview(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie forwarded")
		}
		switch r.URL.Path {
		case "/targets":
			posts.Add(1)
			json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: true, State: "tracking"})
		case "/creators":
			if r.URL.Query().Get("id") != "123" && (r.URL.Query().Get("q") != "" || r.URL.Query().Get("page") != "1") {
				t.Error("UID was not exact search", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(remote.Catalog{Items: []remote.Item{{Kind: "creator", ID: "123", Name: "public", Tracking: true}}, Page: 1, Limit: 18, Total: 1})
		case "/videos":
			json.NewEncoder(w).Encode(remote.Catalog{Items: []remote.Item{}, Page: 1, Limit: 18})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(server.URL)
	page, err := s.Search(context.Background(), "creator", "123", 1)
	if err != nil || page.Items[0].Followed {
		t.Fatal("global tracking treated as personal follow", page, err)
	}
	if posts.Load() != 0 {
		t.Fatal("search submitted tracking")
	}
	f, err := s.Follow(context.Background(), "creator", "123")
	if err != nil || f.Pending || posts.Load() != 1 {
		t.Fatal("follow did not automatically submit", f, err)
	}
	page, err = s.Search(context.Background(), "creator", "123", 1)
	if err != nil || !page.Items[0].Followed {
		t.Fatal("personal follow missing", err)
	}
	d, err := s.RefreshOverview(context.Background())
	if err != nil || d.Public == nil || !d.Public.CatalogBased || d.Public.Summary.Creators != 1 || d.Creators != 1 || posts.Load() != 1 {
		t.Fatal(d, err)
	}
	if err = s.Configure("http://localhost:8182"); err != nil {
		t.Fatal(err)
	}
	if s.Dashboard().Public != nil || s.Dashboard().Pending != 1 {
		t.Fatal("server switch retained cache or lost resubmission")
	}
}

func TestOverviewDropsOldSourceAndKeepsCacheOnReadFailure(t *testing.T) {
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := state{Version: 1, Follows: []Follow{{Kind: "creator", ID: "123", AddedAt: time.Now().UTC()}}, Overview: &remote.Overview{FetchedAt: time.Now().UTC()}}
	if err = store.SaveLibrary(old); err != nil {
		t.Fatal(err)
	}
	s, err := New(store, remote.New(nil))
	if err != nil || s.Dashboard().Public != nil || s.Dashboard().Creators != 1 {
		t.Fatal("old overview retained or local follow lost", err)
	}
	old.Overview.CatalogBased = true
	if err = store.SaveLibrary(old); err != nil {
		t.Fatal(err)
	}
	s, err = New(store, remote.New(nil))
	if err != nil || s.Dashboard().Public != nil {
		t.Fatal("old five-item preview retained without page metadata", err)
	}
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() && r.URL.Path == "/videos" {
			w.WriteHeader(502)
			w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		json.NewEncoder(w).Encode(remote.Catalog{Items: []remote.Item{}, Total: 7, Page: 1, Limit: 18})
	}))
	defer server.Close()
	if err = s.Configure(server.URL); err != nil {
		t.Fatal(err)
	}
	good, err := s.RefreshOverview(context.Background())
	if err != nil || good.Public == nil {
		t.Fatal(err)
	}
	fail.Store(true)
	data, err := s.RefreshOverview(context.Background())
	if err == nil || data.Public == nil || !data.Public.FetchedAt.Equal(good.Public.FetchedAt) || data.Public.Summary.Videos != 7 {
		t.Fatal("failed read overwrote cached overview", data, err)
	}
	reopened, err := New(store, remote.New(nil))
	if err != nil || reopened.Dashboard().Public == nil || !reopened.Dashboard().Public.CatalogBased {
		t.Fatal("catalog cache not preserved on restart", err)
	}
}

func TestDetailRejectsResponseFromPreviousServer(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	started, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"item":{"kind":"creator","id":"123"},"metrics":{"points":[]},"versions":[]}`))
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	_, _ = s.Add("creator", "123")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Detail(ctx, "creator", "123", 1, 0, "30")
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("detail did not start")
	}
	if err := s.Configure("http://localhost:8182"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil || s.Dashboard().Creators != 1 {
		t.Fatal("old-server response accepted or private follow lost", err)
	}
}

func TestDetailOfflineCacheMatchesOnlyDefaultRange(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fail.Load() {
			w.WriteHeader(502)
			w.Write([]byte(`{"error":"offline"}`))
			return
		}
		if r.URL.Path == "/targets" || r.URL.Path == "/api/targets/status" {
			w.Write([]byte(`{"kind":"creator","id":"123","recorded":true,"tracking":true}`))
			return
		}
		w.Write([]byte(`{"item":{"kind":"creator","id":"123","name":"cached UP"},"metrics":{"points":[]},"versions":[],"version_page":1,"version_limit":10}`))
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	if _, err := s.Follow(context.Background(), "creator", "123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(context.Background(), "creator", "123"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	view, err := s.Detail(context.Background(), "creator", "123", 1, 0, "30")
	if err != nil || !view.Cached || view.ReadError == "" || view.LocalFollow == nil || view.Item.Name != "cached UP" {
		t.Fatal(view, err)
	}
	for _, tc := range []struct {
		days string
		page int
		seq  int64
	}{{"7", 1, 0}, {"all", 1, 0}, {"30", 2, 0}, {"30", 1, 1}} {
		if _, err = s.Detail(context.Background(), "creator", "123", tc.page, tc.seq, tc.days); err == nil {
			t.Fatal("default cache used for a different range/page/snapshot", tc)
		}
	}
}

func TestFollowQueuesOfflineAndBackgroundRecovers(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	var ready atomic.Bool
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/targets" {
			calls.Add(1)
			if !ready.Load() {
				w.WriteHeader(502)
				w.Write([]byte(`{"error":"temporary unavailable"}`))
				return
			}
			w.Write([]byte(`{"kind":"video","id":"BV17x411w7KC","tracking":true,"status":"tracking"}`))
			return
		}
		w.Write([]byte(`{"kind":"video","id":"BV17x411w7KC","recorded":false,"tracking":true,"status":"pending"}`))
	}))
	defer server.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(server.URL)
	f, err := s.Follow(context.Background(), "video", "BV17x411w7KC")
	if err != nil || !f.Pending || f.LastError == "" || f.NextAttempt == nil {
		t.Fatal("offline follow not safely queued", f, err)
	}
	if !f.NextAttempt.After(time.Now().Add(45 * time.Second)) {
		t.Fatal("failure did not back off")
	}
	ready.Store(true)
	s.mu.Lock()
	s.state.Follows[0].NextAttempt = nil
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunPending(ctx) }()
	deadline := time.After(3 * time.Second)
	for s.Dashboard().Pending != 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("background submission did not recover")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if calls.Load() != 2 {
		t.Fatal("unexpected duplicate submissions", calls.Load())
	}
}
