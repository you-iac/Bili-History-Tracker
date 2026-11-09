// tracker_test.go 覆盖单个目标的查询、入库与日志记录。

package tracker

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

type fakeQuerier struct {
	videos []source.CreatorVideo
}

func (f *fakeQuerier) GetVideo(_ context.Context, platform, id string) (source.Video, error) {
	view := int64(100)
	raw := []byte(fmt.Sprintf(`{"bvid":%q,"title":"T","desc":"D","pic":"https://i0.hdslb.com/a.jpg",`+
		`"pubdate":1662438000,"duration":10,"dynamic":"","owner":{"mid":1,"name":"U"},`+
		`"stat":{"view":%d},"pages":[]}`, id, view))
	return source.Video{
		Platform: platform, ID: id, Title: "T", Description: "D",
		Creator:         source.Creator{ID: "1", Name: "U"},
		DurationSeconds: 10, ViewCount: &view,
		ObservedAt: time.Now().UTC(), SourceURL: "https://example.test/view", Raw: raw,
	}, nil
}

func (f *fakeQuerier) GetCreator(_ context.Context, platform, id string) (source.CreatorProfile, error) {
	followers := int64(42)
	raw := []byte(`{"card":{"mid":"1","name":"U","sign":"s","face":"https://i0.hdslb.com/f.jpg"},"follower":42}`)
	return source.CreatorProfile{
		Platform: platform, ID: id, Name: "U", Description: "s",
		FollowerCount: &followers,
		ObservedAt:    time.Now().UTC(), SourceURL: "https://example.test/card", Raw: raw,
	}, nil
}

func (f *fakeQuerier) ListCreatorVideos(context.Context, string, string) ([]source.CreatorVideo, error) {
	return f.videos, nil
}

func newTestTracker(t *testing.T) (*Tracker, *storage.Store, *Log) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	log, err := NewLog("", 50)
	if err != nil {
		t.Fatal(err)
	}
	return New(store, &fakeQuerier{}, log, "bilibili"), store, log
}

func TestRunVideoStoresOneVersion(t *testing.T) {
	trk, store, log := newTestTracker(t)
	trk.RunTarget(context.Background(), storage.Target{
		Platform: "bilibili", TargetType: "video", TargetID: "BV1oa41137pj",
		IntervalSeconds: 10800,
	})
	entries := log.Recent(10)
	if len(entries) == 0 || entries[0].Message != "ok" {
		t.Fatalf("expected a successful query log, got %+v", entries)
	}
	if !entries[0].VersionAdded {
		t.Fatal("first observation should add a version")
	}
	count, err := store.VideoVersionCount(context.Background(), "bilibili", "BV1oa41137pj")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("version count = %d, want 1", count)
	}
}

func TestRunCreatorDoesNotSubscribeVideos(t *testing.T) {
	trk, store, log := newTestTracker(t)
	trk.queries = &fakeQuerier{videos: []source.CreatorVideo{
		{ID: "BV1oa41137pj", Title: "T"},
	}}
	trk.RunTarget(context.Background(), storage.Target{
		Platform: "bilibili", TargetType: "creator", TargetID: "1", IntervalSeconds: 10800,
	})
	creatorCount, err := store.CreatorVersionCount(context.Background(), "bilibili", "1")
	if err != nil {
		t.Fatal(err)
	}
	if creatorCount != 1 {
		t.Fatalf("creator version count = %d, want 1", creatorCount)
	}
	videoCount, err := store.VideoVersionCount(context.Background(), "bilibili", "BV1oa41137pj")
	if err != nil {
		t.Fatal(err)
	}
	if videoCount != 0 {
		t.Fatalf("unselected video version count = %d, want 0", videoCount)
	}
	found := false
	for _, entry := range log.Recent(20) {
		if entry.Action == "discover" && entry.Message == "ok" {
			found = true
		}
	}
	if found {
		t.Fatal("creator profile query must not discover or subscribe videos")
	}
	targets, err := store.ListTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("unselected targets created: %+v", targets)
	}
}
