// current_test.go 使用本地HTTP桩验证整页批量历史、本地优先、失败兜底及身份隔离。
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

type currentFunc func(context.Context, string, string) (bilibili.Metadata, error)

func (f currentFunc) Current(c context.Context, k, id string) (bilibili.Metadata, error) {
	return f(c, k, id)
}

type currentStore struct{ data []byte }

func (s *currentStore) LoadLibrary(v any) error {
	if len(s.data) == 0 {
		return nil
	}
	return json.Unmarshal(s.data, v)
}
func (s *currentStore) SaveLibrary(v any) error {
	var err error
	s.data, err = json.Marshal(v)
	return err
}

func currentData(kind, id string) bilibili.Metadata {
	metric := int64(123)
	return bilibili.Metadata{Kind: kind, ID: id, Name: "本地 " + id, Cover: "https://i0.hdslb.com/local.jpg", Metric: &metric, OwnerID: "123", PublishedAt: time.Now().UTC().Truncate(24 * time.Hour).UnixMilli(), At: time.Now().UnixMilli()}
}

func TestLocalPageUsesOneHistoryBatchAndPersistsMetadata(t *testing.T) {
	var localCalls, historyCalls atomic.Int64
	api := currentFunc(func(_ context.Context, kind, id string) (bilibili.Metadata, error) {
		localCalls.Add(1)
		return currentData(kind, id), nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/metrics/batch" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected server query", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		historyCalls.Add(1)
		var input struct {
			Kind     string
			IDs      []string
			Fallback []string `json:"fallback_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Fallback) != 0 || len(input.IDs) != 18 {
			t.Error("successful local metadata must not request fallback", input)
		}
		items := []remote.BatchMetric{}
		day := time.Now().UTC().Truncate(24 * time.Hour)
		for _, id := range input.IDs {
			items = append(items, remote.BatchMetric{Kind: input.Kind, ID: id, Points: []remote.Point{{At: day.AddDate(0, 0, -3).UnixMilli(), Value: 30}, {At: day.UnixMilli(), Value: 10}}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer server.Close()
	store := &currentStore{}
	s, _ := New(store, remote.New(nil), api)
	_ = s.Configure(server.URL)
	ids := []string{}
	for n := 0; n < 18; n++ {
		id := fmt.Sprintf("BV1AAAAAAA%d%d", n/9+1, n%9+1)
		ids = append(ids, id)
		if _, err := s.Add("video", id); err != nil {
			t.Fatal(id, err)
		}
	}
	count, err := s.SyncSummaries(context.Background(), "video", ids...)
	if err != nil || count != 18 || historyCalls.Load() != 1 || localCalls.Load() != 18 {
		t.Fatal(count, err, historyCalls.Load(), localCalls.Load())
	}
	for _, f := range s.List("video", "", 1).Items {
		if f.Summary == nil || f.Summary.MetadataSource != "bilibili" || f.Summary.Metric == nil || *f.Summary.Metric != 123 || f.Summary.Growth == nil || *f.Summary.Growth != -20 || !f.Pending {
			t.Fatal(f)
		}
	}
	reopened, err := New(store, remote.New(nil), api)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.SyncSummaries(context.Background(), "video", ids...); err != nil || localCalls.Load() != 18 {
		t.Fatal("fresh local metadata lost on restart", err, localCalls.Load())
	}
}

func TestLocalFailureFallsBackOnlyForFailedID(t *testing.T) {
	api := currentFunc(func(_ context.Context, kind, id string) (bilibili.Metadata, error) {
		if id == "2" {
			return bilibili.Metadata{}, errors.New("local failure")
		}
		return currentData(kind, id), nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics/batch" || r.Header.Get("Cookie") != "" {
			t.Error(r.URL, r.Header)
		}
		var input struct {
			Fallback []string `json:"fallback_ids"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		if strings.Join(input.Fallback, ",") != "2" {
			t.Error("fallback widened", input)
		}
		metric := int64(500)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"items": []remote.BatchMetric{{Kind: "creator", ID: "1", Points: []remote.Point{}}, {Kind: "creator", ID: "2", Points: []remote.Point{}, Fallback: &remote.Item{Kind: "creator", ID: "2", Name: "服务器兜底", Metric: &metric, Cover: "https://i0.hdslb.com/server.jpg"}}}})
	}))
	defer server.Close()
	s, _ := New(&currentStore{}, remote.New(nil), api)
	s.Configure(server.URL)
	s.Add("creator", "1")
	s.Add("creator", "2")
	count, err := s.SyncSummaries(context.Background(), "creator", "1", "2")
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	names := map[string]string{}
	for _, f := range s.List("creator", "", 1).Items {
		names[f.ID] = f.Summary.Name
		if f.Summary.Growth != nil {
			t.Fatal("empty samples became zero growth")
		}
	}
	if names["1"] != "本地 1" || names["2"] != "服务器兜底" {
		t.Fatal(names)
	}
}

func TestLocalMetadataSurvivesHistoryFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "history offline", 503) }))
	defer server.Close()
	s, _ := New(&currentStore{}, remote.New(nil), currentFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) { return currentData(k, id), nil }))
	s.Configure(server.URL)
	s.Add("creator", "1")
	count, err := s.SyncSummaries(context.Background(), "creator", "1")
	f := s.List("creator", "", 1).Items[0]
	if err == nil || count != 1 || f.Summary == nil || f.Summary.Name != "本地 1" || f.Summary.HistoryError == "" {
		t.Fatal(count, err, f)
	}
}

func TestLocalSyncRejectsChangedFollowOrServerGeneration(t *testing.T) {
	for _, change := range []string{"refollow", "switch-back"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			api := currentFunc(func(ctx context.Context, k, id string) (bilibili.Metadata, error) {
				close(started)
				select {
				case <-release:
					return currentData(k, id), nil
				case <-ctx.Done():
					return bilibili.Metadata{}, ctx.Err()
				}
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"items":[{"kind":"creator","id":"1","points":[]}]}`))
			}))
			defer server.Close()
			s, _ := New(&currentStore{}, remote.New(nil), api)
			s.Configure(server.URL)
			s.Add("creator", "1")
			done := make(chan error, 1)
			go func() { _, err := s.SyncSummaries(context.Background(), "creator", "1"); done <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("not started")
			}
			if change == "refollow" {
				s.Remove("creator", "1")
				s.Add("creator", "1")
			} else {
				s.Configure("http://localhost:8181")
				s.Configure(server.URL)
			}
			close(release)
			err := <-done
			if change == "switch-back" && err == nil || s.state.Follows[0].Summary != nil {
				t.Fatal("stale summary accepted", err)
			}
		})
	}
}

func TestLocalDetailKeepsSnapshotAndSummarySeparate(t *testing.T) {
	var localCalls atomic.Int64
	api := currentFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) {
		localCalls.Add(1)
		return currentData(k, id), nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/history/creator/1" {
			t.Error("detail queried metadata/catalog/state", r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("seq") == "1" {
			w.Write([]byte(`{"item":{"kind":"creator","id":"1","seq":1},"snapshot_seq":1,"snapshot":{"card":{"name":"历史名字","face":"https://i0.hdslb.com/historical.jpg"}}}`))
			return
		}
		w.Write([]byte(`{"item":{"kind":"creator","id":"1","seq":2,"recorded":true},"metrics":{"points":[]},"versions":[{"seq":2,"at":1}],"version_page":1,"version_total":2,"version_limit":10}`))
	}))
	defer server.Close()
	s, _ := New(&currentStore{}, remote.New(nil), api)
	s.Configure(server.URL)
	s.Add("creator", "1")
	view, err := s.Detail(context.Background(), "creator", "1", 1, 0, "30")
	if err != nil || view.Item.Name != "本地 1" || view.Item.Cover != "https://i0.hdslb.com/local.jpg" {
		t.Fatal(view, err)
	}
	old, err := s.Detail(context.Background(), "creator", "1", 1, 1, "30")
	if err != nil || !strings.Contains(string(old.Snapshot), "历史名字") || localCalls.Load() != 1 {
		t.Fatal(old, err, localCalls.Load())
	}
	if got := s.List("creator", "", 1).Items[0].Summary; got == nil || got.Name != "本地 1" {
		t.Fatal("snapshot corrupted current summary", got)
	}
}

func TestLocalCreatorStatsUseFollowedVideos(t *testing.T) {
	s, _ := New(&currentStore{}, remote.New(nil), currentFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) { return currentData(k, id), nil }))
	s.Add("creator", "123")
	s.Add("video", "BV17x411w7KC")
	s.Add("video", "BV1oa41137pj")
	for index, f := range s.state.Follows {
		if f.Kind == "video" {
			i := remote.Item{Kind: f.Kind, ID: f.ID, OwnerID: "123", PublishedAt: time.Now().UnixMilli()}
			value := int64(10)
			i.Metric = &value
			s.state.Follows[index].Summary = &i
		}
	}
	i := followItem(s.List("creator", "", 1).Items[0])
	if i.VideoViews == nil || *i.VideoViews != 20 || i.RecentVideos == nil || *i.RecentVideos != 2 {
		t.Fatal(i)
	}
	s.Remove("video", "BV1oa41137pj")
	i = followItem(s.List("creator", "", 1).Items[0])
	if *i.VideoViews != 10 || *i.RecentVideos != 1 {
		t.Fatal(i)
	}
}

func TestLocalConfigureKeepsCurrentFieldsAndDropsHistory(t *testing.T) {
	s, _ := New(&currentStore{}, remote.New(nil), currentFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) { return currentData(k, id), nil }))
	s.Configure("http://localhost:8181")
	s.Add("creator", "123")
	i := remote.Item{Kind: "creator", ID: "123", Name: "本地名字", MetadataSource: "bilibili", MetadataAt: time.Now().UnixMilli(), Seq: 9, ObservedAt: 100, Recorded: true}
	growth := int64(9)
	i.Growth = &growth
	s.state.Follows[0].Summary = &i
	if err := s.Configure("http://127.0.0.1:8184"); err != nil {
		t.Fatal(err)
	}
	i = *s.state.Follows[0].Summary
	if i.Name != "本地名字" || i.Growth != nil || i.Seq != 0 || i.ObservedAt != 0 || i.Recorded {
		t.Fatal(i)
	}
}

func TestLocalDetailRetainsCachedMetadataWhenBothSourcesFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/metadata/creator/1" {
			http.Error(w, "metadata unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"item":{"kind":"creator","id":"1","seq":3,"observed_at":200,"recorded":true},"metrics":{"points":[]},"versions":[],"version_page":1,"version_total":3,"version_limit":10}`))
	}))
	defer server.Close()
	s, _ := New(&currentStore{}, remote.New(nil), currentFunc(func(context.Context, string, string) (bilibili.Metadata, error) {
		return bilibili.Metadata{}, errors.New("platform unavailable")
	}))
	s.Configure(server.URL)
	s.Add("creator", "1")
	metric := int64(42)
	s.state.Follows[0].Summary = &remote.Item{Kind: "creator", ID: "1", Name: "保留本地名字", Metric: &metric, MetadataSource: "bilibili", MetadataAt: time.Now().Add(-time.Hour).UnixMilli(), Seq: 2, ObservedAt: 100}
	view, err := s.Detail(context.Background(), "creator", "1", 1, 0, "30")
	if err != nil || view.Item.Name != "保留本地名字" || view.Item.Metric == nil || *view.Item.Metric != 42 || view.Item.Seq != 3 || view.ReadError == "" {
		t.Fatal(view, err)
	}
}
