// request_stages_test.go 用可控存储和真实临时库验证阶段预算、部分提交及停机等待。
package tracker

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

type phaseQuerier struct {
	fakeQuerier
	before func(context.Context) error
	after  func()
}

func (q *phaseQuerier) GetCreator(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
	if q.before != nil {
		if err := q.before(ctx); err != nil {
			return source.CreatorProfile{}, err
		}
	}
	value, err := q.fakeQuerier.GetCreator(ctx, platform, id)
	if q.after != nil {
		q.after()
	}
	return value, err
}

func (q *phaseQuerier) GetVideo(ctx context.Context, platform, id string) (source.Video, error) {
	if q.before != nil {
		if err := q.before(ctx); err != nil {
			return source.Video{}, err
		}
	}
	value, err := q.fakeQuerier.GetVideo(ctx, platform, id)
	if q.after != nil {
		q.after()
	}
	return value, err
}

type phaseStore struct {
	requestStore
	at        string
	interrupt func(context.Context) error
}

func (s phaseStore) fail(ctx context.Context, stage string) error {
	if s.at == stage {
		return s.interrupt(ctx)
	}
	return nil
}
func (s phaseStore) TrackingTarget(ctx context.Context, p, kind, id string) (storage.Target, bool, error) {
	if err := s.fail(ctx, "check"); err != nil {
		return storage.Target{}, false, err
	}
	return s.requestStore.TrackingTarget(ctx, p, kind, id)
}
func (s phaseStore) SaveCreator(ctx context.Context, v source.CreatorProfile, fp storage.Fingerprint) (storage.SaveResult, error) {
	if err := s.fail(ctx, "persist"); err != nil {
		return storage.SaveResult{}, err
	}
	return s.requestStore.SaveCreator(ctx, v, fp)
}
func (s phaseStore) SaveVideo(ctx context.Context, v source.Video, fp storage.Fingerprint) (storage.SaveResult, error) {
	if err := s.fail(ctx, "persist"); err != nil {
		return storage.SaveResult{}, err
	}
	return s.requestStore.SaveVideo(ctx, v, fp)
}
func (s phaseStore) RecordMetric(ctx context.Context, p, kind, id, metric string, at time.Time, value int64) error {
	if err := s.fail(ctx, "metric"); err != nil {
		return err
	}
	return s.requestStore.RecordMetric(ctx, p, kind, id, metric, at, value)
}
func (s phaseStore) AddRequestedTarget(ctx context.Context, v storage.Target, at time.Time) (bool, error) {
	if err := s.fail(ctx, "target"); err != nil {
		return false, err
	}
	return s.requestStore.AddRequestedTarget(ctx, v, at)
}
func (s phaseStore) RecordTargetResult(ctx context.Context, v storage.Target, result string, at time.Time) error {
	if err := s.fail(ctx, "outcome"); err != nil {
		return err
	}
	return s.requestStore.RecordTargetResult(ctx, v, result, at)
}

func TestRequestStagesHaveIndependentDeadlines(t *testing.T) {
	for _, stage := range []string{"check", "query", "persist", "metric", "target", "outcome"} {
		t.Run(stage, func(t *testing.T) {
			trk, store, _ := newTestTracker(t)
			trk.budgets = RequestBudgets{Check: time.Second, Query: time.Second, Persist: time.Second, Target: time.Second}
			block := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			want := stage
			switch stage {
			case "check":
				trk.budgets.Check = 10 * time.Millisecond
			case "query":
				trk.budgets.Query = 10 * time.Millisecond
			case "persist", "metric":
				trk.budgets.Persist = 500 * time.Millisecond
				want = "persist"
			case "target", "outcome":
				trk.budgets.Target = 500 * time.Millisecond
				want = "target"
			}
			trk.requests = phaseStore{requestStore: trk.requests, at: stage, interrupt: block}
			if stage == "query" {
				trk.queries = &phaseQuerier{before: block}
			}
			_, err := trk.RequestTarget(context.Background(), "creator", "1")
			var failure *RequestError
			if !errors.As(err, &failure) || failure.Stage != want || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			outcome := "unknown"
			if stage == "check" || stage == "query" {
				outcome = "not_applied"
			}
			if failure.Outcome != outcome {
				t.Fatal(failure)
			}
			count, err := store.CreatorVersionCount(context.Background(), "bilibili", "1")
			if err != nil {
				t.Fatal(err)
			}
			wantVersions := int64(0)
			if stage == "metric" || stage == "target" || stage == "outcome" {
				wantVersions = 1
			}
			if count != wantVersions {
				t.Fatal("partial observation was lost or invented", count)
			}
			_, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
			if err != nil || exists != (stage == "outcome") {
				t.Fatal(exists, err)
			}
			if stage == "outcome" {
				before, _, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
				trk.requests = store
				state, err := trk.RequestTarget(context.Background(), "creator", "1")
				after, _, _ := store.TrackingTarget(context.Background(), "bilibili", "creator", "1")
				if err != nil || state != "existing" || before.NextRunAt != after.NextRunAt {
					t.Fatal("retry changed committed target", state, err)
				}
			}
		})
	}
}

func TestValidObservationSurvivesQueryCancellation(t *testing.T) {
	for kind, id := range map[string]string{"creator": "1", "video": "BV17x411w7KC"} {
		t.Run(kind, func(t *testing.T) {
			trk, store, _ := newTestTracker(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trk.queries = &phaseQuerier{after: cancel}
			state, err := trk.RequestTarget(ctx, kind, id)
			if err != nil || state != "tracking" || ctx.Err() == nil {
				t.Fatal(state, err)
			}
			if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", kind, id); err != nil || !exists {
				t.Fatal(exists, err)
			}
		})
	}
}

func TestStopWaitKeepsDetachedPersistenceAlive(t *testing.T) {
	trk, store, _ := newTestTracker(t)
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	trk.requests = phaseStore{requestStore: store, at: "persist", interrupt: func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	go func() { _, err := trk.RequestTarget(context.Background(), "creator", "1"); returned <- err }()
	<-entered
	trk.Stop()
	waited := make(chan struct{})
	go func() { trk.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("wait returned while persistence was active")
	default:
	}
	if _, err := trk.RequestTarget(context.Background(), "creator", "2"); !errors.Is(err, ErrRequestStopping) {
		t.Fatal(err)
	}
	close(release)
	if err := <-returned; err != nil {
		t.Fatal("shutdown cancelled accepted persistence", err)
	}
	<-waited
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1"); err != nil || !exists {
		t.Fatal(exists, err)
	}
}

func TestStageDoesNotHideUnrelatedStorageFailure(t *testing.T) {
	sentinel := errors.New("database closed")
	ctx, cancel := context.WithCancel(context.Background())
	err := requestStage(ctx, time.Second, "persist", "unknown", func(context.Context) error { cancel(); return errors.Join(ErrTargetStorage, sentinel) })
	if !errors.Is(err, sentinel) || errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	err = requestStage(ctx, time.Second, "persist", "unknown", func(context.Context) error { cancel(); return errors.Join(ErrTargetStorage, sql.ErrTxDone) })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

type interruptedTransactionStore struct {
	requestStore
	db *sql.DB
}

func (s interruptedTransactionStore) SaveCreator(ctx context.Context, profile source.CreatorProfile, _ storage.Fingerprint) (storage.SaveResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storage.SaveResult{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO creators (platform,creator_id,name,first_seen_at,last_referenced_at) VALUES (?,?,?,?,?)`, profile.Platform, profile.ID, profile.Name, 0, 0); err != nil {
		return storage.SaveResult{}, err
	}
	// 在真实事务已写入、尚未提交时耗尽该阶段预算，由 database/sql 取消并回滚。
	<-ctx.Done()
	return storage.SaveResult{}, tx.Commit()
}

func TestPersistenceDeadlineRollsBackActiveTransaction(t *testing.T) {
	trk, store, _ := newTestTracker(t)
	trk.budgets.Persist = 500 * time.Millisecond
	trk.requests = interruptedTransactionStore{requestStore: store, db: store.DB()}
	_, err := trk.RequestTarget(context.Background(), "creator", "1")
	var failure *RequestError
	if !errors.As(err, &failure) || failure.Stage != "persist" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	var rows int
	if err = store.DB().QueryRow("SELECT COUNT(*) FROM creators WHERE creator_id='1'").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("uncommitted row survived cancellation", rows, err)
	}
	if _, exists, err := store.TrackingTarget(context.Background(), "bilibili", "creator", "1"); err != nil || exists {
		t.Fatal(exists, err)
	}
}

func TestStopCancelsQueriesAndWaitsForRunNow(t *testing.T) {
	for _, mode := range []string{"request", "run_now"} {
		t.Run(mode, func(t *testing.T) {
			trk, store, _ := newTestTracker(t)
			entered := make(chan struct{})
			trk.queries = &phaseQuerier{before: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}
			returned := make(chan error, 1)
			if mode == "request" {
				go func() { _, err := trk.RequestTarget(context.Background(), "video", "BV17x411w7KC"); returned <- err }()
			} else {
				trk.RunNow(storage.Target{Platform: "bilibili", TargetType: "video", TargetID: "BV17x411w7KC", IntervalSeconds: 10800})
			}
			<-entered
			trk.Stop()
			trk.Wait()
			if mode == "request" {
				if err := <-returned; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if count, err := store.VideoVersionCount(context.Background(), "bilibili", "BV17x411w7KC"); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if trk.IsRunning("video", "BV17x411w7KC") {
				t.Fatal("worker remained registered")
			}
		})
	}
}

func TestConcurrentPausedTargetIsNotReportedAsCreated(t *testing.T) {
	trk, store, _ := newTestTracker(t)
	trk.requests = phaseStore{requestStore: store, at: "target", interrupt: func(ctx context.Context) error {
		if err := store.AddTarget(ctx, "bilibili", "creator", "1", time.Hour, time.Now()); err != nil {
			return err
		}
		_, err := store.DB().ExecContext(ctx, "UPDATE tracking_targets SET enabled=0 WHERE target_id='1'")
		return err
	}}
	result, err := trk.RequestTarget(context.Background(), "creator", "1")
	if result != "existing" || !errors.Is(err, ErrTargetPaused) {
		t.Fatal(result, err)
	}
}
