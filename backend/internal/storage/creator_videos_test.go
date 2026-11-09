// creator_videos_test.go 验证稳定读取位置及已记录、已跟踪状态的独立性。
package storage

import (
	"context"
	"testing"
	"time"
)

func TestCreatorVideoCursorSurvivesFollowingChanges(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"A", "B", "C"} {
		video := testVideo(id, 100, time.Now())
		video.ID = id
		fp, err := VideoFingerprint(video.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.SaveVideo(ctx, video, fp); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.CreatorVideos(ctx, "bilibili", "122541", "", 1)
	if err != nil || len(first) != 1 || first[0].ID != "A" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err := store.AddTarget(ctx, "bilibili", "video", "A", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreatorVideos(ctx, "bilibili", "122541", "A", 1)
	if err != nil || len(second) != 1 || second[0].ID != "B" {
		t.Fatalf("cursor skipped: %+v %v", second, err)
	}
	if err := store.AddTarget(ctx, "bilibili", "video", "pending", time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	states, err := store.VideoStates(ctx, "bilibili", []string{"A", "B", "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if !states["A"].Recorded || !states["A"].Subscribed || !states["B"].Recorded || states["B"].Tracking || states["pending"].Recorded || !states["pending"].Subscribed {
		t.Fatalf("states conflated: %+v", states)
	}
	if err := store.RemoveTarget(ctx, "bilibili", "video", "A"); err != nil {
		t.Fatal(err)
	}
	states, err = store.VideoStates(ctx, "bilibili", []string{"A"})
	if err != nil || !states["A"].Recorded || states["A"].Tracking {
		t.Fatalf("stopped history lost: %+v %v", states, err)
	}
}
