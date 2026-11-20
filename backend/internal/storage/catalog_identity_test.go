// catalog_identity_test.go 验证轻量公共目录仅取目标身份，普通翻页不依赖历史指标表。
package storage

import (
	"context"
	"testing"
)

func TestCatalogIdentityDoesNotReadMetricsForOrdinarySorts(t *testing.T) {
	s, now := performanceStore(t)
	if _, err := s.db.Exec("DROP TABLE metric_samples"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"video", "creator"} {
		for _, order := range []string{"observed", "popular", "title", "published", "discovered"} {
			page, err := s.CatalogIdentity(context.Background(), "bilibili", CatalogFilter{Kind: kind, Sort: order, Limit: 18}, now)
			if err != nil || len(page.Items) != 18 {
				t.Fatal(kind, order, page, err)
			}
			for _, i := range page.Items {
				if i.Kind != kind || i.ID == "" || i.Metric != nil || i.Cover != "" || i.VideoViews != nil {
					t.Fatal(i)
				}
			}
		}
	}
}

func TestCatalogIdentityHotAndFilters(t *testing.T) {
	s, now := performanceStore(t)
	for _, kind := range []string{"video", "creator"} {
		page, err := s.CatalogIdentity(context.Background(), "bilibili", CatalogFilter{Kind: kind, Sort: "hot", Limit: 18}, now)
		if err != nil || len(page.Items) != 18 {
			t.Fatal(page, err)
		}
		one, err := s.CatalogIdentity(context.Background(), "bilibili", CatalogFilter{Kind: kind, ID: page.Items[0].ID, Page: 100}, now)
		if err != nil || one.Page != 1 || one.Total != 1 || one.Items[0].ID != page.Items[0].ID {
			t.Fatal(one, err)
		}
		wild, err := s.CatalogIdentity(context.Background(), "bilibili", CatalogFilter{Kind: kind, Q: "%"}, now)
		if err != nil || wild.Total != 0 {
			t.Fatal(wild, err)
		}
	}
}
