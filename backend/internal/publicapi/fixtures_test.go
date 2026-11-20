// fixtures_test.go 使用隔离 SQLite 与可控上游，不访问用户数据库或 B站。
package publicapi

import (
	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type guardedQuerier struct {
	calls   atomic.Int64
	creator func(context.Context, string, string) (source.CreatorProfile, error)
	dbPath  string
}

func (q *guardedQuerier) GetVideo(context.Context, string, string) (source.Video, error) {
	q.calls.Add(1)
	return source.Video{}, &source.Error{Kind: source.UpstreamFailure}
}
func (q *guardedQuerier) GetCreator(ctx context.Context, platform, id string) (source.CreatorProfile, error) {
	q.calls.Add(1)
	if q.creator != nil {
		return q.creator(ctx, platform, id)
	}
	return source.CreatorProfile{}, &source.Error{Kind: source.UpstreamFailure}
}
func dashboardServer(t *testing.T) (*Server, *storage.Store, *guardedQuerier) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "public.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	log, err := tracker.NewLog("", 500)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	q := &guardedQuerier{dbPath: dbPath}
	trk := tracker.New(store, q, log, "bilibili")
	server, err := New(store, trk, q, "bilibili", Config{RequestsPerSecond: 10000})
	if err != nil {
		t.Fatal(err)
	}
	return server, store, q
}
func seedDashboard(t *testing.T, store *storage.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	count := int64(100)
	profile := source.CreatorProfile{Platform: "bilibili", ID: "12345", Name: "观测 UP", FollowerCount: &count, ObservedAt: now.AddDate(0, 0, -10), Raw: json.RawMessage(`{"card":{"mid":"12345","name":"观测 UP","sign":"签名"},"follower":100}`)}
	cfp, err := storage.CreatorFingerprint(profile.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveCreator(ctx, profile, cfp); err != nil {
		t.Fatal(err)
	}
	for seq, title := range []string{"旧标题", `<script>alert("x")</script>`} {
		raw, _ := json.Marshal(map[string]any{"bvid": "BV17x411w7KC", "title": title, "desc": "说明", "owner": map[string]any{"mid": 12345}, "pages": []any{}})
		v := source.Video{Platform: "bilibili", ID: "BV17x411w7KC", Title: title, Creator: source.Creator{ID: "12345", Name: "观测 UP"}, ViewCount: &count, ObservedAt: now.Add(time.Duration(seq-1) * time.Hour), Raw: raw}
		fp, e := storage.VideoFingerprint(raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.SaveVideo(ctx, v, fp); e != nil {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{"video", "creator"} {
		id := "BV17x411w7KC"
		metric := "view"
		if kind == "creator" {
			id = "12345"
			metric = "follower"
		}
		if err = store.AddTarget(ctx, "bilibili", kind, id, time.Hour, now); err != nil {
			t.Fatal(err)
		}
		if err = store.RecordTargetResult(ctx, storage.Target{Platform: "bilibili", TargetType: kind, TargetID: id}, "ok", now); err != nil {
			t.Fatal(err)
		}
		for _, offset := range []int{-3, 0} {
			if err = store.RecordMetric(ctx, "bilibili", kind, id, metric, now.Truncate(24*time.Hour).AddDate(0, 0, offset), int64(100-offset)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
