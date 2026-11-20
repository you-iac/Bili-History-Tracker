// detail_query_test.go 验证详情只读取一次资料，任务启停仍实时确认，不触发上游。
package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

type countedHistory struct {
	historyReader
	reads int
}

func (r *countedHistory) Content(ctx context.Context, platform, kind, id string, now time.Time) (storage.ContentItem, error) {
	r.reads++
	return r.historyReader.Content(ctx, platform, kind, id, now)
}

func TestDetailReadsContentOnce(t *testing.T) {
	for _, tc := range []struct{ path, code string }{
		{"/detail/video/BV17x411w7KC", "OK"}, {"/detail/video/BV17x411w7KC?seq=1", "OK"},
		{"/detail/video/BV17x411w7KC?seq=999", "SNAPSHOT_NOT_FOUND"}, {"/detail/video/BV17x411w7KC?days=7&page=2", "OK"},
		{"/detail/creator/12345", "OK"}, {"/detail/creator/987", "DETAIL_NOT_RECORDED"},
		{"/detail/video/BV1oa41137pj", "DETAIL_NOT_RECORDED"}, {"/api/metrics/creator/987?days=30", "DETAIL_NOT_RECORDED"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			seedDashboard(t, store)
			reader := &countedHistory{historyReader: s.reader}
			s.reader = reader
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			var reply struct {
				Code string `json:"code"`
				OK   bool   `json:"ok"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Code != tc.code || reply.OK != (tc.code == "OK") || reader.reads != 1 {
				t.Fatalf("HTTP %d, reads %d: %s", w.Code, reader.reads, w.Body)
			}
			if q.calls.Load() != 0 {
				t.Fatal("read triggered upstream")
			}
		})
	}
}

func TestDetailCachedContentUsesCurrentTargetState(t *testing.T) {
	for _, kind := range []string{"video", "creator"} {
		for _, change := range []string{"paused", "absent", "resumed", "read_failed"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				s, store, q := dashboardServer(t)
				seedDashboard(t, store)
				id := "BV17x411w7KC"
				if kind == "creator" {
					id = "12345"
				}
				if change == "resumed" {
					if _, err := store.DB().Exec("UPDATE tracking_targets SET enabled=0 WHERE target_type=? AND target_id=?", kind, id); err != nil {
						t.Fatal(err)
					}
				}
				s.store = targetLookupBeforeRead{targetLookup: store, before: func(ctx context.Context) {
					var err error
					switch change {
					case "paused":
						_, err = store.DB().ExecContext(ctx, "UPDATE tracking_targets SET enabled=0 WHERE target_type=? AND target_id=?", kind, id)
					case "resumed":
						_, err = store.DB().ExecContext(ctx, "UPDATE tracking_targets SET enabled=1 WHERE target_type=? AND target_id=?", kind, id)
					case "absent":
						err = store.RemoveTarget(ctx, "bilibili", kind, id)
					case "read_failed":
						err = store.Close()
					}
					if err != nil {
						t.Fatal(err)
					}
				}}
				reader := &countedHistory{historyReader: s.reader}
				s.reader = reader
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/detail/"+kind+"/"+id, nil))
				var reply struct {
					OK     bool        `json:"ok"`
					Code   string      `json:"code"`
					Target targetState `json:"target"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reader.reads != 1 {
					t.Fatalf("reads %d, decode %v: %s", reader.reads, err, w.Body)
				}
				if change == "read_failed" {
					if w.Code != 500 || reply.OK || reply.Code != "INTERNAL_ERROR" {
						t.Fatal(w.Code, w.Body.String())
					}
					return
				}
				state, status := change, change
				if change == "resumed" {
					state, status = "active", "ok"
				} else if change == "absent" {
					status = "unfollowed"
				}
				if w.Code != 200 || !reply.OK || !reply.Target.Recorded || reply.Target.TrackingState != state || reply.Target.Status != status || reply.Target.Tracking != (state == "active") || q.calls.Load() != 0 {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestDetailCachedContentPreservesRunningState(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	started, release := make(chan struct{}), make(chan struct{})
	q.creator = func(ctx context.Context, _, _ string) (source.CreatorProfile, error) {
		close(started)
		select {
		case <-ctx.Done():
			return source.CreatorProfile{}, ctx.Err()
		case <-release:
			return source.CreatorProfile{}, errors.New("test release")
		}
	}
	s.tracker.RunNow(storage.Target{Platform: "bilibili", TargetType: "creator", TargetID: "12345", IntervalSeconds: 3600})
	defer func() { close(release); s.tracker.Stop(); s.tracker.Wait() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker did not start")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/detail/creator/12345", nil))
	var reply struct{ Target targetState }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Target.Status != "running" || !reply.Target.Recorded || !reply.Target.Tracking {
		t.Fatal(w.Code, w.Body.String())
	}
}
