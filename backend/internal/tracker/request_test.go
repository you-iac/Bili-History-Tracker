// request_test.go 验证未知目标校验、重复申请不采集或改调度，以及同目标并发限制。
package tracker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"bili-history-tracker/backend/internal/source"
)

type requestQuerier struct {
	fakeQuerier
	calls   atomic.Int64
	fail    bool
	started chan struct{}
	release chan struct{}
}

func (q *requestQuerier) GetVideo(ctx context.Context, p, id string) (source.Video, error) {
	q.calls.Add(1)
	if q.started != nil {
		close(q.started)
		select {
		case <-q.release:
		case <-ctx.Done():
			return source.Video{}, ctx.Err()
		}
	}
	if q.fail {
		return source.Video{}, errors.New("upstream failed")
	}
	return q.fakeQuerier.GetVideo(ctx, p, id)
}
func (q *requestQuerier) GetCreator(ctx context.Context, p, id string) (source.CreatorProfile, error) {
	q.calls.Add(1)
	return q.fakeQuerier.GetCreator(ctx, p, id)
}
func TestRequestTargetValidatesAndReusesTracking(t *testing.T) {
	trk, store, _ := newTestTracker(t)
	q := &requestQuerier{}
	trk.queries = q
	ctx := context.Background()
	for kind, id := range map[string]string{"creator": "1", "video": "BV1oa41137pj"} {
		state, err := trk.RequestTarget(ctx, kind, id)
		if err != nil || state != "tracking" {
			t.Fatalf("first %s: %s %v", kind, state, err)
		}
		before, exists, err := store.TrackingTarget(ctx, "bilibili", kind, id)
		if err != nil || !exists {
			t.Fatal(before, err)
		}
		if before.LastRunAt == nil {
			t.Fatal("initial successful observation missing run time")
		}
		state, err = trk.RequestTarget(ctx, kind, id)
		if err != nil || state != "existing" {
			t.Fatal(state, err)
		}
		after, _, _ := store.TrackingTarget(ctx, "bilibili", kind, id)
		if before.NextRunAt != after.NextRunAt || before.IntervalSeconds != after.IntervalSeconds {
			t.Fatal("repeat request changed schedule")
		}
	}
	if q.calls.Load() != 2 {
		t.Fatal("duplicate triggered platform query", q.calls.Load())
	}
	q.fail = true
	if _, err := trk.RequestTarget(ctx, "video", "BV17x411w7KC"); err == nil {
		t.Fatal("failed query accepted")
	}
	if exists, _ := store.HasTarget(ctx, "bilibili", "video", "BV17x411w7KC"); exists {
		t.Fatal("failed target tracked")
	}
	if count, _ := store.VideoVersionCount(ctx, "bilibili", "BV17x411w7KC"); count != 0 {
		t.Fatal("failed observation persisted")
	}
}
func TestRequestTargetConcurrentSameID(t *testing.T) {
	trk, _, _ := newTestTracker(t)
	q := &requestQuerier{started: make(chan struct{}), release: make(chan struct{})}
	trk.queries = q
	done := make(chan error, 1)
	go func() { _, err := trk.RequestTarget(context.Background(), "video", "BV17x411w7KC"); done <- err }()
	<-q.started
	_, err := trk.RequestTarget(context.Background(), "video", "BV17x411w7KC")
	close(q.release)
	if !errors.Is(err, ErrTargetBusy) {
		t.Fatal("concurrent request not rejected", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if q.calls.Load() != 1 {
		t.Fatal("duplicate upstream requests")
	}
}
