package storage

import (
	"context"
	"testing"
	"time"
)

// TestSaveCreatorFirstInsert 覆盖没有视频引用时首次写入作者资料。
func TestSaveCreatorFirstInsert(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	profile := testProfile("sign", 100, base)
	fp, err := CreatorFingerprint(profile.Raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.SaveCreator(ctx, profile, fp)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !result.VersionAdded || result.Seq != 1 {
		t.Fatalf("first profile insert: %+v", result)
	}
	detail, err := store.LatestCreator(ctx, "bilibili", "122541")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Payload) == 0 || detail.CurrentSeq == nil || *detail.CurrentSeq != 1 {
		t.Fatalf("latest creator payload missing: %+v", detail)
	}
}
