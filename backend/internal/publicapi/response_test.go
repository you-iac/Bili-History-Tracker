// response_test.go 验证正常业务统一200、任务/资料独立、真实故障不被伪装。
package publicapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMissingDetailDistinguishesAbsentActiveAndPaused(t *testing.T) {
	for _, state := range []string{"absent", "active", "paused"} {
		t.Run(state, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			if state != "absent" {
				if err := store.AddTarget(context.Background(), "bilibili", "creator", "987", time.Hour, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if state == "paused" {
				if _, err := store.DB().Exec("UPDATE tracking_targets SET enabled=0 WHERE target_id='987'"); err != nil {
					t.Fatal(err)
				}
			}
			h := s.Handler()
			for _, path := range []string{"/detail/creator/987", "/api/metrics/creator/987?days=30", "/api/targets/status?kind=creator&id=987"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				var reply struct {
					OK     bool         `json:"ok"`
					Code   string       `json:"code"`
					Target *targetState `json:"target"`
					targetState
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
					t.Fatal(w.Code, w.Body.String())
				}
				target := reply.Target
				if target == nil {
					target = &reply.targetState
					if !reply.OK || reply.Code != "OK" {
						t.Fatal(w.Body.String())
					}
				} else if reply.OK || reply.Code != "DETAIL_NOT_RECORDED" {
					t.Fatal(w.Body.String())
				}
				if target.TrackingState != state || target.Recorded || target.Tracking != (state == "active") {
					t.Fatal(w.Body.String())
				}
			}
			if q.calls.Load() != 0 {
				t.Fatal("read triggered upstream")
			}
			if state != "absent" {
				w := postJSON(t, h, `{"type":"creator","id":"987"}`)
				var reply struct {
					OK       bool
					Code     string
					Recorded bool
					Result   string
					Target   *targetState
				}
				_ = json.Unmarshal(w.Body.Bytes(), &reply)
				if w.Code != 200 || state == "paused" && (reply.OK || reply.Code != "TARGET_PAUSED" || reply.Target == nil) || state == "active" && (!reply.OK || reply.Recorded || reply.Result != "existing") {
					t.Fatal(w.Code, w.Body.String())
				}
				if q.calls.Load() != 0 {
					t.Fatal("existing target resampled")
				}
			}
		})
	}
}

func TestBusinessFailureAndInternalFailureRemainDistinct(t *testing.T) {
	s, store, _ := dashboardServer(t)
	h := s.Handler()
	w := postJSON(t, h, `{"type":"video","id":"BV17x411w7KC"}`)
	var reply struct {
		OK   bool
		Code string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	if w.Code != 200 || reply.OK || reply.Code != "TARGET_SUBMISSION_FAILED" {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/targets/status?kind=creator&id=1", "/detail/creator/1"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		_ = json.Unmarshal(w.Body.Bytes(), &reply)
		if w.Code != 500 || reply.OK || reply.Code != "INTERNAL_ERROR" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestSnapshotAbsenceAndUpstreamFailureUseJSON(t *testing.T) {
	s, store, _ := dashboardServer(t)
	seedDashboard(t, store)
	h := s.Handler()
	for path, code := range map[string]string{"/detail/video/BV17x411w7KC?seq=999": "SNAPSHOT_NOT_FOUND", "/api/v1/platforms/bilibili/videos/BV17x411w7KC": "UPSTREAM_UNAVAILABLE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var reply struct {
			OK   bool
			Code string
		}
		_ = json.Unmarshal(w.Body.Bytes(), &reply)
		if w.Code != 200 || reply.OK || reply.Code != code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestResponseEnvelopePreservesLargeIntegerSnapshot(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 200, map[string]any{"snapshot": json.RawMessage(`{"mid":9223372036854775806,"zero":0}`)})
	var response struct {
		Snapshot struct {
			Mid  int64
			Zero int64
		}
		OK bool
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || !response.OK || response.Snapshot.Mid != 9223372036854775806 || response.Snapshot.Zero != 0 {
		t.Fatal(w.Body.String())
	}
}
