// ingest_test.go 验证归属校验先于写入、重复导入幂等及已停止视频恢复关注。
package tracker

import (
	"context"
	"testing"
	"time"
)

func TestIngestValidatesOwnerAndResumesRecordedVideo(t *testing.T) {
	trk, store, _ := newTestTracker(t)
	ctx := context.Background()
	id := "BV1oa41137pj"
	status, err := trk.IngestVideo(ctx, "2", id, time.Hour)
	if err != nil || status != "rejected" {
		t.Fatalf("owner mismatch: %s %v", status, err)
	}
	if count, _ := store.VideoVersionCount(ctx, "bilibili", id); count != 0 {
		t.Fatal("rejected video was saved")
	}
	if target, _ := store.HasTarget(ctx, "bilibili", "video", id); target {
		t.Fatal("rejected video was tracked")
	}
	status, err = trk.IngestVideo(ctx, "1", id, time.Hour)
	if err != nil || status != "added" {
		t.Fatalf("first import: %s %v", status, err)
	}
	status, err = trk.IngestVideo(ctx, "2", id, time.Hour)
	if err != nil || status != "rejected" {
		t.Fatalf("tracked video owner bypass: %s %v", status, err)
	}
	status, err = trk.IngestVideo(ctx, "1", id, time.Hour)
	if err != nil || status != "existing" {
		t.Fatalf("duplicate import: %s %v", status, err)
	}
	if err := store.RemoveTarget(ctx, "bilibili", "video", id); err != nil {
		t.Fatal(err)
	}
	status, err = trk.IngestVideo(ctx, "1", id, time.Hour)
	if err != nil || status != "existing" {
		t.Fatalf("restore recorded video: %s %v", status, err)
	}
	if count, _ := store.VideoVersionCount(ctx, "bilibili", id); count != 1 {
		t.Fatalf("duplicate versions: %d", count)
	}
	states, err := store.VideoStates(ctx, "bilibili", []string{id})
	if err != nil || !states[id].Recorded || !states[id].Subscribed {
		t.Fatalf("restored state=%+v %v", states, err)
	}
}
