// request_stages_test.go 验证阶段协议、错误优先级、确认丢失和批量接纳截止，不等待真实20秒。
package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/tracker"
)

func TestRequestFailureClassifiesCauseBeforeStorageMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		code   string
		status int
	}{
		{"cancelled", context.Canceled, "REQUEST_TIMEOUT", 504},
		{"deadline", context.DeadlineExceeded, "REQUEST_TIMEOUT", 504},
		{"database", errors.New("database closed"), "INTERNAL_ERROR", 500},
	} {
		for _, stage := range []string{"check", "persist", "target", "confirm"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				phaseErr := &tracker.RequestError{Stage: stage, Outcome: "unknown", Cause: errors.Join(tracker.ErrTargetStorage, tc.cause)}
				result := requestFailure(targetResult{}, phaseErr)
				if result.Code != tc.code || result.HTTPStatus != tc.status || result.Stage != stage || result.Outcome != "unknown" || result.OK {
					t.Fatal(result)
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(encoded, &fields) != nil {
					t.Fatal(string(encoded))
				}
				if _, exists := fields["tracking"]; exists {
					t.Fatal("unknown target serialized as tracking=false", string(encoded))
				}
			})
		}
	}
}

func TestConfirmTimeoutKeepsCommittedTargetAndRetryDoesNotResample(t *testing.T) {
	for _, entry := range []string{"single", "batch"} {
		t.Run(entry, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			q.creator = creatorSuccess
			s.confirmTimeout = 10 * time.Millisecond
			s.store = targetLookupBeforeRead{s.store, func(ctx context.Context) { <-ctx.Done() }}
			body := `{"type":"creator","id":"1"}`
			if entry == "batch" {
				body = `{"targets":[` + body + `]}`
			}
			w := postJSON(t, s.Handler(), body)
			var reply struct {
				targetResult
				Results []targetResult
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			result := reply.targetResult
			if entry == "batch" {
				if len(reply.Results) != 1 {
					t.Fatal(w.Body.String())
				}
				result = reply.Results[0]
			}
			if result.OK || result.Code != "REQUEST_TIMEOUT" || result.Stage != "confirm" || result.Outcome != "unknown" || result.Target != nil {
				t.Fatal(w.Body.String())
			}
			before, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
			if err != nil || !exists || !before.Enabled {
				t.Fatal(before, exists, err)
			}
			s.store = store
			w = postJSON(t, s.Handler(), `{"type":"creator","id":"1"}`)
			var confirmed struct {
				OK              bool
				Result, Outcome string
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &confirmed) != nil || !confirmed.OK || confirmed.Result != "existing" || confirmed.Outcome != "confirmed" {
				t.Fatal(w.Body.String())
			}
			after, _, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
			if q.calls.Load() != 1 || before.NextRunAt != after.NextRunAt {
				t.Fatal("retry resampled or changed schedule", q.calls.Load())
			}
		})
	}
}

func TestBatchDeadlineFinishesValidObservationButDoesNotStartTail(t *testing.T) {
	s, store, q := dashboardServer(t)
	s.admissionTimeout = 10 * time.Millisecond
	q.creator = func(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
		value, err := creatorSuccess(context.Background(), platform, id)
		<-ctx.Done()
		return value, err
	}
	w := postJSON(t, s.Handler(), `{"targets":[{"type":"creator","id":"1"},{"type":"creator","id":"2"}]}`)
	var reply struct{ Results []targetResult }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || len(reply.Results) != 2 || !reply.Results[0].OK || reply.Results[1].Code != "REQUEST_TIMEOUT" || reply.Results[1].Stage != "check" || reply.Results[1].Outcome != "not_applied" || q.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), q.calls.Load())
	}
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1"); err != nil || !exists {
		t.Fatal(exists, err)
	}
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "2"); err != nil || exists {
		t.Fatal(exists, err)
	}
}

func TestQueryAndDatabaseFailuresCarryDifferentStages(t *testing.T) {
	for _, mode := range []string{"query_timeout", "query_failure", "persist_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			q.creator = func(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
				if mode == "query_timeout" {
					return source.CreatorProfile{}, context.DeadlineExceeded
				}
				if mode == "query_failure" {
					return source.CreatorProfile{}, errors.New("upstream failed")
				}
				value, err := creatorSuccess(ctx, platform, id)
				if closeErr := store.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				return value, err
			}
			w := postJSON(t, s.Handler(), `{"type":"creator","id":"1"}`)
			var result targetResult
			if json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal(w.Body.String())
			}
			stage, code, status, outcome := "query", "TARGET_SUBMISSION_FAILED", 200, "not_applied"
			if mode == "query_timeout" {
				code = "REQUEST_TIMEOUT"
			}
			if mode == "persist_failure" {
				stage, code, status, outcome = "persist", "INTERNAL_ERROR", 500, "unknown"
			}
			if w.Code != status || result.Stage != stage || result.Code != code || result.Outcome != outcome || result.OK {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestCancelledRequestCanFinishValidatedObservation(t *testing.T) {
	s, store, q := dashboardServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.creator = func(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
		value, err := creatorSuccess(ctx, platform, id)
		cancel()
		return value, err
	}
	// 直接验证 submit；真实客户端已断开时不保证还能收到 JSON。
	result := s.submit(ctx, targetInput{Kind: "creator", ID: "1"})
	if !result.OK || result.Outcome != "confirmed" {
		t.Fatal(result)
	}
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1"); err != nil || !exists {
		t.Fatal(exists, err)
	}
}
