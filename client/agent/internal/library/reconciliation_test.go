// reconciliation_test.go 验证漏跟踪恢复、业务失败边界、持久化退避和旧请求隔离。
// 后台覆盖已确认关注在服务器丢失任务后的恢复，避免只测待提交队列。
package library

import (
	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetailRepairsOldConfirmationAndKeepsCachedData(t *testing.T) {
	var posts, checks atomic.Int64
	var active atomic.Bool
	target := func() remote.Status {
		state := "absent"
		if active.Load() {
			state = "active"
		}
		return remote.Status{Kind: "creator", ID: "123", TrackingState: state, Tracking: active.Load(), State: "pending"}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/targets/status":
			checks.Add(1)
			json.NewEncoder(w).Encode(target())
		case "/targets":
			posts.Add(1)
			active.Store(true)
			json.NewEncoder(w).Encode(target())
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": "DETAIL_NOT_RECORDED", "error": "无成功资料", "target": target()})
		}
	}))
	defer server.Close()
	s, _ := New(&failingStore{}, remote.New(nil))
	_ = s.Configure(server.URL)
	_, _ = s.Add("creator", "123")
	now := time.Now()
	s.state.Follows[0].Pending = false
	s.state.Follows[0].CheckedAt = &now
	s.state.Follows[0].ServerURL = server.URL
	s.state.Follows[0].Status = &remote.Status{Kind: "creator", ID: "123", Tracking: true, TrackingState: "active", Recorded: true}
	s.state.Follows[0].Data = &remote.Detail{Item: remote.Item{Kind: "creator", ID: "123", Name: "保留的历史", Seq: 1, Recorded: true}}
	for range 2 {
		view, err := s.Detail(context.Background(), "creator", "123", 1, 0, "30")
		if err != nil || !view.Cached || view.ReadCode != "DETAIL_NOT_RECORDED" || view.Target == nil || !view.Target.Tracking || view.LocalFollow.Pending || view.Item.Name != "保留的历史" {
			t.Fatal(view, err)
		}
	}
	if posts.Load() != 1 || checks.Load() != 1 {
		t.Fatal("duplicate request", posts.Load(), checks.Load())
	}
	if _, err := s.Refresh(context.Background(), "creator", "123"); err != nil {
		t.Fatal(err)
	}
	if s.List("creator", "", 1).Items[0].Data == nil {
		t.Fatal("empty remote state erased cache")
	}
}

func TestReconciliationNeverSubmitsPausedActiveOrUnknown(t *testing.T) {
	for _, mode := range []string{"paused", "active", "offline", "limited"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" {
					posts.Add(1)
				}
				if mode == "offline" || mode == "limited" {
					code := "TARGET_SUBMISSION_FAILED"
					if mode == "limited" {
						code = "RATE_LIMITED"
					}
					json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": code, "error": "未完成"})
					return
				}
				json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: mode == "active", TrackingState: mode, State: mode})
			}))
			defer server.Close()
			s, _ := New(&failingStore{}, remote.New(nil))
			_ = s.Configure(server.URL)
			_, _ = s.Add("creator", "123")
			f, err := s.Reconcile(context.Background(), "creator", "123", true)
			if posts.Load() != 0 {
				t.Fatal("unexpected submission")
			}
			if mode == "paused" && (err != nil || f.Pending || f.LastCode != "TARGET_PAUSED") {
				t.Fatal(f, err)
			}
			if mode == "active" && (err != nil || f.Pending || !f.Status.Tracking) {
				t.Fatal(f, err)
			}
			if (mode == "offline" || mode == "limited") && (err == nil || !f.Pending || f.NextAttempt == nil) {
				t.Fatal(f, err)
			}
		})
	}
}

func TestFailuresBecomeManualAndPersistAcrossRestart(t *testing.T) {
	var posts atomic.Int64
	var recovered atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			posts.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": "TARGET_SUBMISSION_FAILED", "error": "无法验证"})
			return
		}
		json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: recovered.Load()})
	}))
	defer server.Close()
	store, _ := localstate.New(t.TempDir())
	s, _ := New(store, remote.New(nil))
	_ = s.Configure(server.URL)
	_, _ = s.Add("creator", "123")
	for range 6 {
		if _, err := s.Reconcile(context.Background(), "creator", "123", true); err == nil {
			t.Fatal("failure accepted")
		}
	}
	s, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	f := s.List("creator", "", 1).Items[0]
	if !f.NeedsAttention || f.Attempts != 6 || f.NextAttempt == nil || f.LastCode != "TARGET_SUBMISSION_FAILED" {
		t.Fatal(f)
	}
	_, _ = s.Reconcile(context.Background(), "creator", "123", false)
	if posts.Load() != 6 {
		t.Fatal("manual state retried automatically")
	}
	recovered.Store(true)
	f, err = s.Reconcile(context.Background(), "creator", "123", true)
	if err != nil || f.NeedsAttention || f.Pending || posts.Load() != 6 {
		t.Fatal(f, err)
	}
}

func TestSlowStatusCannotSubmitAfterRemoveOrSwitchBack(t *testing.T) {
	for _, change := range []string{"remove", "replace", "switch-back"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var posts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" {
					posts.Add(1)
				}
				close(started)
				<-release
				json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123"})
			}))
			defer server.Close()
			s, _ := New(&failingStore{}, remote.New(nil))
			_ = s.Configure(server.URL)
			_, _ = s.Add("creator", "123")
			done := make(chan error, 1)
			go func() { _, err := s.Reconcile(context.Background(), "creator", "123", true); done <- err }()
			<-started
			switch change {
			case "remove":
				_ = s.Remove("creator", "123")
			case "replace":
				_ = s.Remove("creator", "123")
				_, _ = s.Add("creator", "123")
			case "switch-back":
				_ = s.Configure("http://127.0.0.1:1")
				_ = s.Configure(server.URL)
			}
			close(release)
			if err := <-done; err == nil || posts.Load() != 0 {
				t.Fatal("stale status submitted", err, posts.Load())
			}
		})
	}
}

func TestConcurrentChecksAndPublicMissingDetailDoNotSubmitTwice(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var checks, posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/targets/status":
			checks.Add(1)
			close(started)
			<-release
			json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123"})
		case "/targets":
			posts.Add(1)
			json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: true})
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": "DETAIL_NOT_RECORDED", "error": "无资料"})
		}
	}))
	defer server.Close()
	s, _ := New(&failingStore{}, remote.New(nil))
	_ = s.Configure(server.URL)
	view, err := s.Detail(context.Background(), "creator", "123", 1, 0, "30")
	if err != nil || view.LocalFollow != nil || checks.Load() != 0 || posts.Load() != 0 {
		t.Fatal(view, err)
	}
	_, _ = s.Add("creator", "123")
	done := make(chan error, 1)
	go func() { _, err := s.Reconcile(context.Background(), "creator", "123", true); done <- err }()
	<-started
	if _, err := s.Reconcile(context.Background(), "creator", "123", true); err == nil {
		t.Fatal("concurrent forced check not rejected")
	}
	close(release)
	if err := <-done; err != nil || checks.Load() != 1 || posts.Load() != 1 {
		t.Fatal(err, checks.Load(), posts.Load())
	}
}

func TestBackgroundRechecksPreviouslyConfirmedFollowAfterRestart(t *testing.T) {
	var active atomic.Bool
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			posts.Add(1)
			active.Store(true)
		}
		json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: active.Load()})
	}))
	defer server.Close()
	store, _ := localstate.New(t.TempDir())
	oldTime := time.Now().Add(-time.Hour)
	if err := store.SaveLibrary(state{Version: 1, ServerURL: server.URL, Follows: []Follow{{Kind: "creator", ID: "123", AddedAt: oldTime, CheckedAt: &oldTime, Status: &remote.Status{Kind: "creator", ID: "123", Tracking: true}, ServerURL: server.URL}}}); err != nil {
		t.Fatal(err)
	}
	s, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.RunPending(ctx) }()
	for {
		f := s.List("creator", "", 1).Items[0]
		if posts.Load() == 1 && f.CheckedAt != nil && !f.Pending {
			break
		}
		select {
		case <-ctx.Done():
			<-done
			t.Fatal("old confirmation never rechecked")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if posts.Load() != 1 {
		t.Fatal("duplicate restoration", posts.Load())
	}
}
