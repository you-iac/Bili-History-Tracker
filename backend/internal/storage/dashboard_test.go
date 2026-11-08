// dashboard_test.go 验证看板口径、分页、趋势缺口及采集失败/恢复的持久化。
package storage

import (
	"context"
	"testing"
	"time"
)

func TestDashboardQueries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	video := testVideo("测试视频", 100, now.Add(-time.Hour))
	fp, err := VideoFingerprint(video.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveVideo(ctx, video, fp); err != nil {
		t.Fatal(err)
	}
	profile := testProfile("签名", 200, now.Add(-time.Minute))
	cfp, _ := CreatorFingerprint(profile.Raw)
	if _, err = s.SaveCreator(ctx, profile, cfp); err != nil {
		t.Fatal(err)
	}
	for _, target := range []Target{{Platform: "bilibili", TargetType: "video", TargetID: video.ID}, {Platform: "bilibili", TargetType: "creator", TargetID: profile.ID}, {Platform: "bilibili", TargetType: "video", TargetID: "BV17x411w7KC"}} {
		if err = s.AddTarget(ctx, "bilibili", target.TargetType, target.TargetID, time.Hour, now); err != nil {
			t.Fatal(err)
		}
	}
	day := now.Truncate(24 * time.Hour)
	for _, sample := range []struct {
		at    time.Time
		value int64
	}{{day.AddDate(0, 0, -3), 80}, {day, 100}} {
		if err = s.RecordMetric(ctx, "bilibili", "video", video.ID, "view", sample.at, sample.value); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []CatalogFilter{{Kind: "video"}, {Kind: "creator"}, {OnlyTargets: true}, {Kind: "video", Sort: "growth"}, {Q: "测试"}, {Q: "%"}, {Kind: "video", OwnerID: profile.ID}} {
		page, err := s.Catalog(ctx, "bilibili", f, now)
		if err != nil {
			t.Fatalf("filter %+v: %v", f, err)
		}
		if f.Sort == "growth" && (len(page.Items) != 1 || page.Items[0].Growth == nil || *page.Items[0].Growth != 20) {
			t.Fatalf("growth: %+v", page)
		}
		if f.Q == "%" && page.Total != 0 {
			t.Fatal("search wildcard was not escaped")
		}
	}
	page, err := s.Catalog(ctx, "bilibili", CatalogFilter{OnlyTargets: true}, now)
	if err != nil || page.Total != 3 {
		t.Fatalf("pending target lost: %+v %v", page, err)
	}
	summary, err := s.Summary(ctx, "bilibili", now)
	if err != nil || summary.Videos != 1 || summary.Creators != 1 || summary.Updates != 0 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	series, err := s.Metrics(ctx, "bilibili", "video", video.ID, 7, now)
	if err != nil || len(series.Points) != 2 || series.Points[0].Value != 80 {
		t.Fatalf("metric gaps: %+v %v", series, err)
	}
	versions, err := s.Versions(ctx, "bilibili", "", "", "", 7, 1, 10)
	if err != nil || versions.Total != 0 {
		t.Fatalf("first capture treated as update: %+v %v", versions, err)
	}
	versions, err = s.Versions(ctx, "bilibili", "video", video.ID, "", 0, 1, 10)
	if err != nil || versions.Total != 1 {
		t.Fatalf("history: %+v %v", versions, err)
	}
	if _, err = s.CollectionSeries(ctx, "bilibili", now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadSnapshot(ctx, "bilibili", "video", video.ID, 0); err != nil {
		t.Fatal(err)
	}
}

func TestTargetFailureRecovery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	target := Target{Platform: "bilibili", TargetType: "creator", TargetID: "122541"}
	if err := s.AddTarget(ctx, "bilibili", target.TargetType, target.TargetID, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{"rate_limited", "upstream_failure"} {
		if err := s.RecordTargetResult(ctx, target, result, now); err != nil {
			t.Fatal(err)
		}
	}
	issues, err := s.Catalog(ctx, "bilibili", CatalogFilter{OnlyTargets: true, State: "issues"}, now)
	if err != nil || issues.Total != 1 || issues.Items[0].Failures != 2 {
		t.Fatalf("issues: %+v %v", issues, err)
	}
	if err := s.RecordTargetResult(ctx, target, "ok", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	issues, err = s.Catalog(ctx, "bilibili", CatalogFilter{OnlyTargets: true, State: "issues"}, now.Add(time.Minute))
	if err != nil || issues.Total != 0 {
		t.Fatalf("recovered still counted: %+v %v", issues, err)
	}
	recovered, err := s.Catalog(ctx, "bilibili", CatalogFilter{OnlyTargets: true, State: "recovered"}, now.Add(time.Minute))
	if err != nil || recovered.Total != 1 || recovered.Items[0].Failures != 0 || recovered.Items[0].FailureReason != "upstream_failure" {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	stale, err := s.Catalog(ctx, "bilibili", CatalogFilter{OnlyTargets: true, State: "issues"}, now.Add(4*time.Hour))
	if err != nil || stale.Total != 1 || stale.Items[0].Status != "stale" {
		t.Fatalf("stale: %+v %v", stale, err)
	}
}
