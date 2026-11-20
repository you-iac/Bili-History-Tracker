// content_test.go 验证单目标摘要与目录口径一致、平台隔离及窗口边界，不使用用户数据库。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
)

func TestContentMatchesCatalog(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	since := now.Truncate(24*time.Hour).AddDate(0, 0, -6)
	video := testVideo("旧标题", 100, now.AddDate(0, 0, -10))
	for _, title := range []string{"旧标题", "当前标题"} {
		video.Title = title
		if title == "当前标题" {
			video = testVideo(title, 0, now.Add(-time.Hour))
			video.PublishedAt = &since
		}
		fp, err := VideoFingerprint(video.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SaveVideo(ctx, video, fp); err != nil {
			t.Fatal(err)
		}
	}
	unknown := testVideo("未知播放", 0, now.Add(-time.Hour))
	unknown.ID, unknown.ViewCount, unknown.PublishedAt = "BVunknown000", nil, nil
	future := testVideo("未来投稿", 7, now.Add(-time.Hour))
	future.ID = "BVfuture0000"
	at := now.Add(time.Hour)
	future.PublishedAt = &at
	placeholder := testVideo("占位作者", 5, now.Add(-time.Hour))
	placeholder.ID, placeholder.Creator.ID = "BVplaceholder", "987"
	otherPlatform := testVideo("其他平台同 ID", 999, now.Add(-time.Hour))
	otherPlatform.Platform = "other"
	for _, v := range []source.Video{unknown, future, placeholder, otherPlatform} {
		fp, err := VideoFingerprint(v.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SaveVideo(ctx, v, fp); err != nil {
			t.Fatal(err)
		}
	}
	profile := testProfile("完整资料", 0, now)
	fp, err := CreatorFingerprint(profile.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCreator(ctx, profile, fp); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []struct{ kind, id, metric string }{{"video", video.ID, "view"}, {"creator", profile.ID, "follower"}} {
		for _, sample := range []struct {
			at    time.Time
			value int64
		}{{since.Add(-time.Millisecond), 900}, {since, 100}, {now, 40}, {now.Add(time.Millisecond), 800}} {
			if err = s.RecordMetric(ctx, "bilibili", subject.kind, subject.id, subject.metric, sample.at, sample.value); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.AddTarget(ctx, "bilibili", subject.kind, subject.id, time.Hour, now); err != nil {
			t.Fatal(err)
		}
	}
	// 一个样本、同 ID 的其他指标和其他平台的样本不能污染当前目标增长。
	for _, sample := range []struct{ platform, kind, id, metric string }{
		{"bilibili", "video", unknown.ID, "view"}, {"bilibili", "video", video.ID, "follower"},
		{"other", "video", video.ID, "view"},
	} {
		if err = s.RecordMetric(ctx, sample.platform, sample.kind, sample.id, sample.metric, since, 1); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T) {
		t.Helper()
		for _, subject := range []struct{ platform, kind, id string }{
			{"bilibili", "video", video.ID}, {"bilibili", "video", unknown.ID},
			{"bilibili", "video", future.ID}, {"bilibili", "creator", profile.ID},
			{"bilibili", "creator", "987"}, {"other", "video", video.ID},
		} {
			got, e := s.Content(ctx, subject.platform, subject.kind, subject.id, now)
			page, pe := s.Catalog(ctx, subject.platform, CatalogFilter{Kind: subject.kind, ID: subject.id, Limit: 1}, now)
			if e != nil || pe != nil || len(page.Items) != 1 || !reflect.DeepEqual(got, page.Items[0]) {
				t.Fatalf("%+v: content=%+v (%v), catalog=%+v (%v)", subject, got, e, page, pe)
			}
		}
	}
	check(t)
	got, err := s.Content(ctx, "bilibili", "video", video.ID, now)
	if err != nil || got.Seq != 2 || got.Cover == "" || got.Metric == nil || *got.Metric != 0 || got.Growth == nil || *got.Growth != -60 || got.GrowthFrom != since.UnixMilli() || got.GrowthTo != now.UnixMilli() {
		t.Fatalf("video fields/window: %+v %v", got, err)
	}
	got, err = s.Content(ctx, "bilibili", "creator", profile.ID, now)
	if err != nil || got.VideoCount != 3 || got.VideoViews == nil || *got.VideoViews != 7 || got.RecentVideos == nil || *got.RecentVideos != 1 {
		t.Fatalf("creator aggregates: %+v %v", got, err)
	}
	got, err = s.Content(ctx, "bilibili", "video", unknown.ID, now)
	if err != nil || got.Metric != nil || got.Growth != nil || got.PublishedAt != 0 {
		t.Fatalf("unknown values: %+v %v", got, err)
	}
	for _, state := range []string{"error", "recovered", "stale", "paused", "absent"} {
		t.Run(state, func(t *testing.T) {
			target := Target{Platform: "bilibili", TargetType: "video", TargetID: video.ID}
			switch state {
			case "error":
				err = s.RecordTargetResult(ctx, target, "upstream_failure", now)
			case "recovered":
				err = s.RecordTargetResult(ctx, target, "ok", now)
			case "stale":
				now = now.Add(4 * time.Hour)
			case "paused":
				_, err = s.db.Exec("UPDATE tracking_targets SET enabled=0 WHERE platform='bilibili' AND target_type='video' AND target_id=?", video.ID)
			case "absent":
				err = s.RemoveTarget(ctx, "bilibili", "video", video.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			check(t)
		})
	}
}

func TestContentMissingInvalidCanceledAndReadOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.AddTarget(ctx, "bilibili", "video", "missing", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := s.db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"video", "creator"} {
		if _, err := s.Content(ctx, "bilibili", kind, "missing", now); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s: expected no recorded row, got %v", kind, err)
		}
	}
	if _, err := s.Content(ctx, "bilibili", "other", "missing", now); err == nil {
		t.Fatal("invalid kind accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Content(canceled, "bilibili", "video", "missing", now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup: %v", err)
	}
	if err := s.db.QueryRow("SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatalf("read wrote data: %d -> %d (%v)", before, after, err)
	}
}
