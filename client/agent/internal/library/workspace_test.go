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
			if r.URL.Query().Get("id") != "123" {
				t.Error("UID was not exact search", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(remote.Catalog{Items: []remote.Item{{Kind: "creator", ID: "123", Name: "public", Tracking: true}}, Page: 1, Limit: 18, Total: 1})
		case "/":
			json.NewEncoder(w).Encode(remote.Overview{FetchedAt: time.Now().UTC()})
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
	if err != nil || d.Public == nil || d.Creators != 1 {
		t.Fatal(d, err)
	}
	if err = s.Configure("http://localhost:8182"); err != nil {
		t.Fatal(err)
	}
	if s.Dashboard().Public != nil || s.Dashboard().Pending != 1 {
		t.Fatal("server switch retained cache or lost resubmission")
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
