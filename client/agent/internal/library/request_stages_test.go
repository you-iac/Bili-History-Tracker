// request_stages_test.go 验证阶段错误重启保留，未知结果在下次核对后确认，不重复申请。
package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"bili-history-tracker/client/agent/internal/localstate"
	"bili-history-tracker/client/agent/internal/remote"
)

func TestUnknownSubmissionResultPersistsAndRechecksBeforeRetry(t *testing.T) {
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			posts.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "code": "REQUEST_TIMEOUT", "stage": "confirm", "outcome": "unknown", "error": "确认超时"})
			return
		}
		json.NewEncoder(w).Encode(remote.Status{Kind: "creator", ID: "123", Tracking: posts.Load() > 0})
	}))
	defer server.Close()
	store, err := localstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(server.URL); err != nil {
		t.Fatal(err)
	}
	f, err := s.Follow(context.Background(), "creator", "123")
	if err != nil || !f.Pending || f.LastStage != "confirm" || f.LastOutcome != "unknown" || f.LastCode != "REQUEST_TIMEOUT" {
		t.Fatal(f, err)
	}
	s, err = New(store, remote.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	f = s.List("creator", "", 1).Items[0]
	if f.LastStage != "confirm" || f.LastOutcome != "unknown" || f.Attempts != 1 {
		t.Fatal(f)
	}
	f, err = s.Reconcile(context.Background(), "creator", "123", true)
	if err != nil || f.Pending || f.Status == nil || !f.Status.Tracking || f.LastStage != "" || f.LastOutcome != "" || f.Attempts != 0 || posts.Load() != 1 {
		t.Fatal(f, err, posts.Load())
	}
}
