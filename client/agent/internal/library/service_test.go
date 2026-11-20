// service_test.go 验证关注/配置持久化、网络失败保留数据、切服隔离及写入失败回滚。
package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestLocalFollowsPersistAndRemoteFailureKeepsCache(t *testing.T) {
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var tracking atomic.Bool
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "" {
			t.Error("forwarded local cookie")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/targets":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			tracking.Store(true)
			json.NewEncoder(w).Encode(remote.Status{Kind: "video", ID: "BV17x411w7KC", Tracking: true, State: "tracking"})
		case "/api/targets/status":
			json.NewEncoder(w).Encode(remote.Status{Kind: "video", ID: "BV17x411w7KC", Recorded: true, Tracking: tracking.Load()})
		default:
			json.NewEncoder(w).Encode(remote.Detail{Item: remote.Item{Kind: "video", ID: "BV17x411w7KC", Name: "已缓存的标题", Recorded: true}})
		}
	}))
	defer server.Close()
	s, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Add("video", "https://www.bilibili.com/video/BV17x411w7KC/?x=1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Add("video", "BV17x411w7KC")
	if s.List("video", "", 1).Total != 1 {
		t.Fatal("duplicate local follow")
	}
	if err = s.Configure(server.URL); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(context.Background(), "video", "BV17x411w7KC"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Refresh(context.Background(), "video", "BV17x411w7KC"); err != nil {
		t.Fatal(err)
	}
	s, err = New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Connection() != server.URL || s.List("video", "缓存", 1).Total != 1 {
		t.Fatal("restart lost configuration/cache")
	}
	server.Close()
	if _, err = s.Refresh(context.Background(), "video", "BV17x411w7KC"); err == nil {
		t.Fatal("offline request succeeded")
	}
	f := s.List("video", "", 1).Items[0]
	if f.Data == nil || f.LastError == "" {
		t.Fatal("failure erased cache or reason")
	}
	before := requests.Load()
	if err = s.Remove("video", "BV17x411w7KC"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before || !tracking.Load() {
		t.Fatal("local unfollow altered server")
	}
}

type failingStore struct{ fail bool }

func (*failingStore) LoadLibrary(any) error { return nil }
func (s *failingStore) SaveLibrary(any) error {
	if s.fail {
		return errors.New("disk full")
	}
	return nil
}
func TestFailedPersistenceAndServerChange(t *testing.T) {
	store := &failingStore{}
	s, _ := New(store, remote.New(nil))
	_, _ = s.Add("creator", "123")
	store.fail = true
	if s.Remove("creator", "123") == nil || s.List("creator", "", 1).Total != 1 {
		t.Fatal("failed write changed memory")
	}
	if s.Configure("http://127.0.0.1:8182") == nil || s.Connection() != "" {
		t.Fatal("failed config write changed memory")
	}
	store.fail = false
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"kind":"creator","id":"123","recorded":false,"tracking":false}`))
	}))
	defer server.Close()
	_ = s.Configure(server.URL)
	done := make(chan error, 1)
	go func() { _, err := s.Refresh(context.Background(), "creator", "123"); done <- err }()
	<-started
	_ = s.Configure("http://127.0.0.1:8182")
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale response accepted after server change")
	}
	if s.List("creator", "", 1).Items[0].Status != nil {
		t.Fatal("old server metadata leaked")
	}
}
