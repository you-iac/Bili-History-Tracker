// storage_test.go 覆盖内容指纹与版本写入的核心规则。

package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testVideo(title string, view int64, observed time.Time) source.Video {
	raw := []byte(fmt.Sprintf(`{"bvid":"BV1oa41137pj","title":%q,"desc":"desc",`+
		`"pic":"https://i0.hdslb.com/bfs/archive/a.jpg@480w_270h","pubdate":1662438000,`+
		`"duration":1045,"dynamic":"dyn","owner":{"mid":122541,"name":"UP"},`+
		`"stat":{"view":%d},"pages":[{"page":1,"cid":2,"part":"P1","duration":1045}],"state":0}`,
		title, view))
	published := time.Unix(1662438000, 0).UTC()
	return source.Video{
		Platform:        "bilibili",
		ID:              "BV1oa41137pj",
		Title:           title,
		Description:     "desc",
		Creator:         source.Creator{ID: "122541", Name: "UP"},
		PublishedAt:     &published,
		DurationSeconds: 1045,
		ViewCount:       &view,
		ObservedAt:      observed,
		SourceURL:       "https://api.bilibili.com/x/web-interface/view?bvid=BV1oa41137pj",
		Raw:             raw,
	}
}

func testProfile(sign string, followers int64, observed time.Time) source.CreatorProfile {
	raw := []byte(fmt.Sprintf(`{"card":{"mid":"122541","name":"UP","sign":%q,`+
		`"face":"https://i0.hdslb.com/bfs/face/a.jpg"},"follower":%d}`, sign, followers))
	return source.CreatorProfile{
		Platform:      "bilibili",
		ID:            "122541",
		Name:          "UP",
		Description:   sign,
		AvatarURL:     "https://i0.hdslb.com/bfs/face/a.jpg",
		FollowerCount: &followers,
		ObservedAt:    observed,
		SourceURL:     "https://api.bilibili.com/x/web-interface/card?mid=122541",
		Raw:           raw,
	}
}

func TestVideoFingerprintIgnoresCounters(t *testing.T) {
	first, err := VideoFingerprint(testVideo("标题", 100, time.Now()).Raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := VideoFingerprint(testVideo("标题", 999999, time.Now()).Raw)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("counter change altered fingerprint: %s != %s", first, second)
	}
	third, err := VideoFingerprint(testVideo("新标题", 100, time.Now()).Raw)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("title change did not alter fingerprint")
	}
}

func TestVideoFingerprintNormalizesCoverURL(t *testing.T) {
	base := testVideo("标题", 1, time.Now())
	changed := base
	changed.Raw = []byte(`{"bvid":"BV1oa41137pj","title":"标题","desc":"desc",` +
		`"pic":"https://i0.hdslb.com/bfs/archive/a.jpg?sign=abc","pubdate":1662438000,` +
		`"duration":1045,"dynamic":"dyn","owner":{"mid":122541,"name":"UP"},` +
		`"stat":{"view":1},"pages":[{"page":1,"cid":2,"part":"P1","duration":1045}]}`)
	first, err := VideoFingerprint(base.Raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := VideoFingerprint(changed.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("cover signing parameters should not change the fingerprint")
	}
}

func TestSaveVideoVersionRules(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	video := testVideo("A", 100, base)
	fp, err := VideoFingerprint(video.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveVideo(ctx, video, fp); err != nil {
		t.Fatal(err)
	}
	assertVersionCount(t, store, "BV1oa41137pj", 1)

	// Same content, later observation: no version, counters refreshed.
	video = testVideo("A", 200, base.Add(time.Hour))
	fp, _ = VideoFingerprint(video.Raw)
	result, err := store.SaveVideo(ctx, video, fp)
	if err != nil {
		t.Fatal(err)
	}
	if result.VersionAdded || result.Seq != 1 {
		t.Fatalf("same content should not add a version: %+v", result)
	}
	assertVersionCount(t, store, "BV1oa41137pj", 1)

	// Content change: new version.
	video = testVideo("B", 300, base.Add(2*time.Hour))
	fp, _ = VideoFingerprint(video.Raw)
	result, err = store.SaveVideo(ctx, video, fp)
	if err != nil {
		t.Fatal(err)
	}
	if !result.VersionAdded || result.Seq != 2 {
		t.Fatalf("content change should add seq=2: %+v", result)
	}
	assertVersionCount(t, store, "BV1oa41137pj", 2)

	// Late observation: nothing is written.
	video = testVideo("C", 400, base.Add(30*time.Minute))
	fp, _ = VideoFingerprint(video.Raw)
	result, err = store.SaveVideo(ctx, video, fp)
	if err != nil {
		t.Fatal(err)
	}
	if result.VersionAdded {
		t.Fatal("late observation should be discarded")
	}
	assertVersionCount(t, store, "BV1oa41137pj", 2)

	// A -> B -> A: reverted content still appends a version.
	video = testVideo("A", 500, base.Add(3*time.Hour))
	fp, _ = VideoFingerprint(video.Raw)
	result, err = store.SaveVideo(ctx, video, fp)
	if err != nil {
		t.Fatal(err)
	}
	if !result.VersionAdded || result.Seq != 3 {
		t.Fatalf("revert should add seq=3: %+v", result)
	}
	assertVersionCount(t, store, "BV1oa41137pj", 3)

	// Invariants: current hash/seq point at the newest version.
	assertInvariants(t, store)
}

func TestSaveCreatorCreatesReferenceThenProfile(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Video reference creates a creator row without profile pointers.
	video := testVideo("A", 1, base)
	fp, _ := VideoFingerprint(video.Raw)
	if _, err := store.SaveVideo(ctx, video, fp); err != nil {
		t.Fatal(err)
	}
	profile := testProfile("sign", 100, base.Add(time.Minute))
	profileFP, _ := CreatorFingerprint(profile.Raw)
	result, err := store.SaveCreator(ctx, profile, profileFP)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created {
		t.Fatalf("creator row already exists from the video reference: %+v", result)
	}
	if !result.VersionAdded || result.Seq != 1 {
		t.Fatalf("first profile should add seq=1: %+v", result)
	}
	assertCreatorVersionCount(t, store, 1)

	// Same profile: no new version, follower refreshed.
	profile = testProfile("sign", 200, base.Add(2*time.Minute))
	profileFP, _ = CreatorFingerprint(profile.Raw)
	result, err = store.SaveCreator(ctx, profile, profileFP)
	if err != nil {
		t.Fatal(err)
	}
	if result.VersionAdded {
		t.Fatalf("same profile should not add a version: %+v", result)
	}
	assertCreatorVersionCount(t, store, 1)

	// Changed signature: new version.
	profile = testProfile("new sign", 300, base.Add(3*time.Minute))
	profileFP, _ = CreatorFingerprint(profile.Raw)
	result, err = store.SaveCreator(ctx, profile, profileFP)
	if err != nil {
		t.Fatal(err)
	}
	if !result.VersionAdded || result.Seq != 2 {
		t.Fatalf("profile change should add seq=2: %+v", result)
	}
	assertCreatorVersionCount(t, store, 2)
	assertInvariants(t, store)
}

func assertVersionCount(t *testing.T, store *Store, videoID string, want int64) {
	t.Helper()
	got, err := store.VideoVersionCount(context.Background(), "bilibili", videoID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("version count = %d, want %d", got, want)
	}
}

func assertCreatorVersionCount(t *testing.T, store *Store, want int64) {
	t.Helper()
	got, err := store.CreatorVersionCount(context.Background(), "bilibili", "122541")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("creator version count = %d, want %d", got, want)
	}
}

func assertInvariants(t *testing.T, store *Store) {
	t.Helper()
	var mismatch int
	if err := store.DB().QueryRow(`
SELECT COUNT(*) FROM videos AS v
JOIN video_versions AS x
  ON x.platform = v.platform AND x.video_id = v.video_id AND x.seq = v.current_seq
WHERE v.current_hash != x.content_hash`).Scan(&mismatch); err != nil {
		t.Fatal(err)
	}
	if mismatch != 0 {
		t.Fatalf("current_hash does not match the current version row: %d", mismatch)
	}
	var wrongSeq int
	if err := store.DB().QueryRow(`
SELECT COUNT(*) FROM videos AS v
WHERE v.current_seq != (SELECT MAX(seq) FROM video_versions x
                        WHERE x.platform = v.platform AND x.video_id = v.video_id)`).Scan(&wrongSeq); err != nil {
		t.Fatal(err)
	}
	if wrongSeq != 0 {
		t.Fatalf("current_seq is not the newest seq: %d", wrongSeq)
	}
}
