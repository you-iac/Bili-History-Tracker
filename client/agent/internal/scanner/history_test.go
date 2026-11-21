// history_test.go 验证历史同步去重、30 天清理、分页与 UP 聚合。
package scanner

import (
	"context"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

func TestHistorySyncDedupRetentionAndAggregate(t *testing.T) {
	now := time.Now()
	store := &memoryStore{cookie: "SESSDATA=test"}
	api := &fakeAPI{
		page: func(context.Context, int) (bilibili.Page, error) { return bilibili.Page{}, nil },
		history: func(context.Context, string, int64, int64, bilibili.Keys) (bilibili.HistoryPage, error) {
			return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
				{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Hour).Unix()},
				{BVID: "BV2", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-10 * 24 * time.Hour).Unix()},
				{BVID: "BV3", AuthorID: "2", AuthorName: "旧UP", ViewedAt: now.Add(-40 * 24 * time.Hour).Unix()},
			}}, nil
		},
	}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.HistorySync(context.Background())
	if err != nil || result.Added != 3 || result.Removed != 1 || result.Total != 2 {
		t.Fatal(result, err)
	}
	if got := s.HistoryVideos(7, 1, 20); got.Total != 1 || got.Items[0].BVID != "BV1" {
		t.Fatal(got)
	}
	if got := s.HistoryVideos(30, 1, 20); got.Total != 2 {
		t.Fatal(got)
	}
	if got := s.HistoryCreators(30, 1, 20); got.Total != 1 || got.Items[0].ID != "1" || got.Items[0].Videos != 2 {
		t.Fatal(got)
	}
}

func TestHistorySyncKeepsLatestDuplicate(t *testing.T) {
	now := time.Now()
	store := &memoryStore{cookie: "SESSDATA=test"}
	api := &fakeAPI{
		page: func(context.Context, int) (bilibili.Page, error) { return bilibili.Page{}, nil },
		history: func(_ context.Context, _ string, max, _ int64, _ bilibili.Keys) (bilibili.HistoryPage, error) {
			if max == 0 {
				return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
					{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-2 * time.Hour).Unix()},
				}, Max: 1, ViewAt: 2, More: true}, nil
			}
			return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
				{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Hour).Unix()},
			}}, nil
		},
	}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.HistorySync(context.Background())
	if err != nil || result.Added != 1 || result.Updated != 1 || result.Total != 1 {
		t.Fatal(result, err)
	}
	if got := s.HistoryVideos(1, 1, 20); got.Total != 1 || got.Items[0].ViewedAt != now.Add(-time.Hour).Unix() {
		t.Fatal(got)
	}
}
