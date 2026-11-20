// images_test.go 验证按版本持久缓存、并发去重、切服隔离与失败退避；图片解析不调用平台采集。
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestImagesCacheByVersionAndPersist(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/detail/video/BV17x411w7KC" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected image lookup", r.Method, r.URL.Path)
		}
		seq := r.URL.Query().Get("seq")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"item":{"kind":"video","id":"BV17x411w7KC","seq":2},"snapshot_seq":%s,"snapshot":{"pic":"http://i0.hdslb.com/cover-%s.jpg"}}`, seq, seq)
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	for _, seq := range []int64{1, 1, 2, 1} {
		result, err := s.Image(context.Background(), "video", "BV17x411w7KC", seq)
		if err != nil || !result.Available || result.URL != fmt.Sprintf("https://i0.hdslb.com/cover-%d.jpg", seq) {
			t.Fatal(result, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("cached image requeried", calls.Load())
	}
	reopened, _ := New(store, remote.New(nil))
	result, err := reopened.Image(context.Background(), "video", "BV17x411w7KC", 1)
	if err != nil || !result.Cached || calls.Load() != 2 {
		t.Fatal("cache did not survive restart", result, err)
	}
	_ = reopened.Configure("http://localhost:8182")
	if len(reopened.state.Images) != 0 {
		t.Fatal("different server retained image cache")
	}
}

func TestImagesSingleFlightFailureAndServerSwitch(t *testing.T) {
	store, _ := localstate.New(t.TempDir())
	var calls atomic.Int64
	var blocked atomic.Bool
	started, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if blocked.Load() {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		time.Sleep(15 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"item":{"kind":"creator","id":"123","seq":1},"snapshot_seq":1,"snapshot":{"card":{"face":"https://i0.hdslb.com/a.jpg"}}}`))
	}))
	defer upstream.Close()
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(upstream.URL)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if result, err := s.Image(context.Background(), "creator", "123", 1); err != nil || !result.Available {
				t.Error(result, err)
			}
		})
	}
	group.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate lookups", calls.Load())
	}
	blocked.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Image(ctx, "creator", "123", 2); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("lookup did not start")
	}
	_ = s.Configure("http://localhost:8182")
	close(release)
	if err := <-done; err == nil || len(s.state.Images) != 0 {
		t.Fatal("old server image accepted", err)
	}
	failedCalls := 0
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		json.NewEncoder(w).Encode(map[string]string{"error": "temporary failure"})
	}))
	defer failed.Close()
	_ = s.Configure(failed.URL)
	for range 2 {
		if _, err := s.Image(context.Background(), "creator", "123", 1); err == nil {
			t.Fatal("failure treated as image success")
		}
	}
	if failedCalls != 1 {
		t.Fatal("temporary errors did not back off", failedCalls)
	}
}
