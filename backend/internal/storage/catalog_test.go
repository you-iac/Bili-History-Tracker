// catalog_test.go 验证客户端目录排序跨页、UP 聚合口径及未知增量保留。
package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCatalogGlobalOrderAndCreatorPublicationMetrics(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	var sum int64
	for n := 1; n <= 23; n++ {
		v := testVideo(fmt.Sprintf("视频 %02d", n), int64(n), now)
		v.ID = fmt.Sprintf("BVtest%06d", n)
		published := now.Add(-time.Duration(n) * time.Hour)
		v.PublishedAt = &published
		if n == 23 {
			v.ViewCount = nil
		} else {
			sum += int64(n)
		}
		fp, _ := VideoFingerprint(v.Raw)
		if _, err := s.SaveVideo(ctx, v, fp); err != nil {
			t.Fatal(err)
		}
	}
	profile := testProfile("", 100, now)
	fp, _ := CreatorFingerprint(profile.Raw)
	if _, err := s.SaveCreator(ctx, profile, fp); err != nil {
		t.Fatal(err)
	}
	p, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "creator"}, now)
	if err != nil || len(p.Items) != 1 {
		t.Fatal(p, err)
	}
	i := p.Items[0]
	if i.VideoViews == nil || *i.VideoViews != sum || i.RecentVideos == nil || *i.RecentVideos != 23 || i.PublishedAt != now.Add(-time.Hour).UnixMilli() {
		t.Fatalf("wrong aggregation: %+v", i)
	}
	first, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "video", OwnerID: profile.ID, Sort: "views", Limit: 18}, now)
	if err != nil || first.Total != 23 || first.Items[0].ID != "BVtest000022" {
		t.Fatal(first, err)
	}
	last, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "video", OwnerID: profile.ID, Sort: "views", Limit: 18, Page: 2}, now)
	if err != nil || len(last.Items) != 5 || last.Items[4].Metric != nil {
		t.Fatal("unknown must be last", last, err)
	}
	hot, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "video", Sort: "hot"}, now)
	if err != nil || hot.Total != 23 {
		t.Fatal("hot sort discarded unknown samples", hot, err)
	}
	latest, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "video", Sort: "published"}, now)
	if err != nil || latest.Items[0].ID != "BVtest000001" {
		t.Fatal(latest, err)
	}
	other, err := s.Catalog(ctx, "bilibili", CatalogFilter{Kind: "video", OwnerID: "999"}, now)
	if err != nil || other.Total != 0 {
		t.Fatal("owner filter leaked", other, err)
	}
}
