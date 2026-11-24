// history_test.go 验证历史同步去重、30 天清理、分页与 UP 聚合。
package scanner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
)

func TestHistoryFilterBeforePagination(t *testing.T) {
	store := &memoryStore{cookie: "SESSDATA=test"}
	excludedVideos, excludedCreators := map[string]bool{}, map[string]bool{}
	var items []bilibili.HistoryItem
	for i := range 65 {
		item := bilibili.HistoryItem{BVID: fmt.Sprintf("BV%03d", i), AuthorID: fmt.Sprint(i + 1), ViewedAt: time.Now().Unix() - int64(i)}
		items = append(items, item)
		if i < 20 {
			excludedVideos[item.BVID], excludedCreators[item.AuthorID] = true, true
		}
	}
	api := &fakeAPI{history: func(context.Context, string, int64, int64, bilibili.Keys) (bilibili.HistoryPage, error) {
		return bilibili.HistoryPage{Items: items}, nil
	}}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.HistorySync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{12, 20, 30} {
		includeVideo := func(id string) bool { return !excludedVideos[id] }
		includeCreator := func(id string) bool { return !excludedCreators[id] }
		videos := s.HistoryVideosFiltered(30, 2, limit, includeVideo)
		creators := s.HistoryCreatorsFiltered(30, 2, limit, includeCreator)
		if videos.Total != 45 || videos.Page != 2 || videos.Limit != limit || len(videos.Items) != min(limit, 45-limit) || videos.Items[0].BVID != items[20+limit].BVID {
			t.Fatalf("video filter/page: %+v", videos)
		}
		if creators.Total != 45 || creators.Page != 2 || creators.Limit != limit || len(creators.Items) != min(limit, 45-limit) || creators.Items[0].ID != items[20+limit].AuthorID {
			t.Fatalf("creator filter/page: %+v", creators)
		}
		last := s.HistoryVideosFiltered(30, 99, limit, includeVideo)
		if last.Page != (45+limit-1)/limit || len(last.Items) == 0 {
			t.Fatal("last page not clamped", last)
		}
	}
	if got := s.HistoryVideosFiltered(30, 99, 12, func(string) bool { return false }); got.Total != 0 || got.Page != 1 || len(got.Items) != 0 {
		t.Fatal("empty videos", got)
	}
	if got := s.HistoryCreatorsFiltered(30, 99, 12, func(string) bool { return false }); got.Total != 0 || got.Page != 1 || len(got.Items) != 0 {
		t.Fatal("empty creators", got)
	}
	if got := s.HistoryVideos(30, 1, 20); got.Total != 65 || len(got.Items) != 20 {
		t.Fatal("unfiltered history changed", got)
	}
}

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
	if err != nil || result.Added != 2 || result.Updated != 0 || result.Total != 2 {
		t.Fatal(result, err)
	}
	got := s.HistoryVideos(1, 1, 20)
	if got.Total != 1 || got.Items[0].Views != 2 || got.Items[0].ViewedAt != now.Add(-time.Hour).Unix() {
		t.Fatal(got)
	}
}

func TestHistorySyncIncrementalStopsEarly(t *testing.T) {
	now := time.Now()
	store := &memoryStore{cookie: "SESSDATA=test"}
	calls := 0
	api := &fakeAPI{
		page: func(context.Context, int) (bilibili.Page, error) { return bilibili.Page{}, nil },
		history: func(_ context.Context, _ string, max, _ int64, _ bilibili.Keys) (bilibili.HistoryPage, error) {
			calls++
			if calls == 1 {
				return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
					{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Hour).Unix()},
					{BVID: "BV2", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-2 * time.Hour).Unix()},
					{BVID: "BV3", AuthorID: "2", AuthorName: "UP2", ViewedAt: now.Add(-3 * time.Hour).Unix()},
				}}, nil
			}
			return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
				{BVID: "BV4", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Minute).Unix()},
				{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Hour).Unix()},
				{BVID: "BV2", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-2 * time.Hour).Unix()},
			}, Max: 2, ViewAt: 3, More: true}, nil
		},
	}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.HistorySync(context.Background())
	if err != nil || first.Added != 3 || first.Pages != 1 || first.Total != 3 {
		t.Fatal(first, err)
	}
	second, err := s.HistorySync(context.Background())
	if err != nil || second.Added != 1 || second.Pages != 1 || second.Total != 4 {
		t.Fatal(second, err)
	}
	if calls != 2 {
		t.Fatal("incremental sync requested an extra page:", calls)
	}
}

func TestCheckInteractionsUpdatesLocalHistory(t *testing.T) {
	now := time.Now()
	store := &memoryStore{cookie: "SESSDATA=test"}
	api := &fakeAPI{
		page: func(context.Context, int) (bilibili.Page, error) { return bilibili.Page{}, nil },
		history: func(context.Context, string, int64, int64, bilibili.Keys) (bilibili.HistoryPage, error) {
			return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
				{BVID: "BV1", AuthorID: "1", AuthorName: "UP", ViewedAt: now.Add(-time.Hour).Unix()},
				{BVID: "BV2", AuthorID: "2", AuthorName: "UP2", ViewedAt: now.Add(-2 * time.Hour).Unix()},
			}}, nil
		},
		like: func(_ context.Context, _ string, bvid string, _ bilibili.Keys) (bool, error) {
			return bvid == "BV1", nil
		},
		coin: func(_ context.Context, _ string, bvid string, _ bilibili.Keys) (int, error) {
			if bvid == "BV2" {
				return 2, nil
			}
			return 0, nil
		},
		favorite: func(context.Context, string, int64, bilibili.Keys) (map[string]bool, error) {
			return map[string]bool{"BV2": true}, nil
		},
	}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HistorySync(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := s.CheckInteractions(context.Background(), 60)
	if err != nil || result.Checked != 2 || result.Liked != 1 || result.Coined != 1 || result.Favorited != 1 {
		t.Fatal(result, err)
	}
	page := s.HistoryVideos(30, 1, 20)
	byID := map[string]HistoryVideo{}
	for _, item := range page.Items {
		byID[item.BVID] = item
	}
	if !byID["BV1"].Like || byID["BV1"].Coin != 0 || byID["BV1"].InteractionCheckedAt == 0 {
		t.Fatal(byID["BV1"])
	}
	if byID["BV2"].Like || byID["BV2"].Coin != 2 || !byID["BV2"].Favorite {
		t.Fatal(byID["BV2"])
	}
}

func TestCheckInteractionsFailureConsumesBudgetAndSavesBackoff(t *testing.T) {
	now := time.Now()
	store := &memoryStore{cookie: "SESSDATA=test"}
	attempts := 0
	api := &fakeAPI{
		page: func(context.Context, int) (bilibili.Page, error) { return bilibili.Page{}, nil },
		history: func(context.Context, string, int64, int64, bilibili.Keys) (bilibili.HistoryPage, error) {
			return bilibili.HistoryPage{Items: []bilibili.HistoryItem{
				{BVID: "BV1", AuthorID: "1", ViewedAt: now.Add(-time.Hour).Unix()},
				{BVID: "BV2", AuthorID: "1", ViewedAt: now.Add(-2 * time.Hour).Unix()},
				{BVID: "BV3", AuthorID: "1", ViewedAt: now.Add(-3 * time.Hour).Unix()},
			}}, nil
		},
		like: func(context.Context, string, string, bilibili.Keys) (bool, error) {
			attempts++
			return false, errors.New("boom")
		},
		favorite: func(context.Context, string, int64, bilibili.Keys) (map[string]bool, error) {
			return map[string]bool{}, nil
		},
	}
	s, err := New(api, store, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HistorySync(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := s.CheckInteractions(context.Background(), 2)
	if err != nil || attempts != 2 || result.Failed != 2 || result.Checked != 0 {
		t.Fatal(result, attempts, err)
	}
	var state historyState
	if err := store.LoadHistory(&state); err != nil {
		t.Fatal(err)
	}
	backoff := 0
	for _, item := range state.Items {
		if item.InteractionNextAttempt > 0 {
			backoff++
		}
	}
	if backoff != 2 {
		t.Fatal("backoff not saved for failed candidates:", backoff)
	}
}
