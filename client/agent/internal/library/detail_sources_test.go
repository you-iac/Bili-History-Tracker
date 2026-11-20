// detail_sources_test.go 验证平台/服务器/缓存的分离、失败退避与迟到结果隔离。
package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/remote"
)

type metadataFunc func(context.Context, string, string) (bilibili.Metadata, error)

func (f metadataFunc) Current(c context.Context, k, id string) (bilibili.Metadata, error) {
	return f(c, k, id)
}

type detailMemory struct{ data []byte }

func (m *detailMemory) LoadLibrary(v any) error {
	if len(m.data) == 0 {
		return nil
	}
	return json.Unmarshal(m.data, v)
}
func (m *detailMemory) SaveLibrary(v any) error {
	b, e := json.Marshal(v)
	if e == nil {
		m.data = b
	}
	return e
}

func TestCurrentPlatformCacheAndNoServerRead(t *testing.T) {
	var calls atomic.Int32
	memory := &detailMemory{}
	reader := metadataFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) {
		calls.Add(1)
		zero := int64(0)
		return bilibili.Metadata{Kind: k, ID: id, Name: "live", Metric: &zero, ObservedAt: time.Now().UnixMilli()}, nil
	})
	s, _ := New(memory, remote.New(nil), reader)
	_, _ = s.Add("creator", "123")
	cache, err := s.CurrentCache("creator", "123")
	if err != nil || cache.Available || cache.LocalFollow == nil {
		t.Fatal(cache, err)
	}
	v, err := s.Current(context.Background(), "creator", "123", false)
	if err != nil || !v.Available || v.Source != "platform" || v.Item.Seq != 0 || v.Item.Recorded || v.Item.Metric == nil || *v.Item.Metric != 0 {
		t.Fatal(v, err)
	}
	v, err = s.Current(context.Background(), "creator", "123", false)
	if err != nil || !v.Cached || calls.Load() != 1 {
		t.Fatal(v, err, calls.Load())
	}
	s2, err := New(memory, remote.New(nil), reader)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s2.CurrentCache("creator", "123")
	if err != nil || !v.Available || v.Source != "platform" || v.LocalFollow == nil {
		t.Fatal("restart", v, err)
	}
	if err = s2.Configure("http://127.0.0.1:8181"); err != nil {
		t.Fatal(err)
	}
	v, _ = s2.CurrentCache("creator", "123")
	if !v.Available || v.Source != "platform" {
		t.Fatal("switch erased public platform cache", v)
	}
}

func TestCurrentFailureFallbackMatrix(t *testing.T) {
	for _, serverMode := range []string{"saved", "missing", "unavailable"} {
		for _, cached := range []bool{false, true} {
			t.Run(serverMode+map[bool]string{true: "-cache", false: "-empty"}[cached], func(t *testing.T) {
				var platformCalls, serverCalls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					serverCalls.Add(1)
					if r.URL.Path != "/api/detail/creator/123/saved" || r.Method != "GET" {
						t.Error("unexpected call", r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					switch serverMode {
					case "saved":
						w.Write([]byte(`{"item":{"kind":"creator","id":"123","name":"saved","recorded":true,"seq":7,"observed_at":100,"metric":99},"description":"saved description","snapshot_at":50}`))
					case "missing":
						w.Write([]byte(`{"ok":false,"code":"DETAIL_NOT_RECORDED","error":"missing"}`))
					default:
						w.WriteHeader(500)
						w.Write([]byte(`{"error":"offline"}`))
					}
				}))
				defer server.Close()
				reader := metadataFunc(func(context.Context, string, string) (bilibili.Metadata, error) {
					platformCalls.Add(1)
					return bilibili.Metadata{}, errors.New("platform unavailable")
				})
				s, _ := New(&detailMemory{}, remote.New(nil), reader)
				_ = s.Configure(server.URL)
				if cached {
					s.state.Current = map[string]CurrentView{"creator:123": {Available: true, Source: "platform", Item: remote.Item{Kind: "creator", ID: "123", Name: "cached", ObservedAt: 10}}}
				}
				v, err := s.Current(context.Background(), "creator", "123", true)
				if err != nil || v.Available != (serverMode == "saved" || cached) || v.FallbackReason == "" {
					t.Fatal(v, err)
				}
				if serverMode == "saved" && (v.Source != "server" || v.Item.Seq != 7 || v.Item.ObservedAt != 100 || v.SnapshotAt != 50 || v.ServerURL != server.URL) {
					t.Fatal(v)
				}
				if serverMode != "saved" && (v.ReadError == "" || cached && (!v.Cached || v.Item.Name != "cached" || v.Source != "platform" || v.Item.ObservedAt != 10)) {
					t.Fatal(v)
				}
				_, _ = s.Current(context.Background(), "creator", "123", true)
				if serverCalls.Load() != 1 || platformCalls.Load() != 1 {
					t.Fatal("failure retry storm", serverCalls.Load(), platformCalls.Load())
				}
				_ = s.Configure("http://127.0.0.1:8181")
				if serverMode == "saved" {
					v, _ = s.CurrentCache("creator", "123")
					if v.Available {
						t.Fatal("server cache leaked after switch")
					}
				}
			})
		}
	}
}

func TestCurrentInFlightAndLateResult(t *testing.T) {
	for _, change := range []string{"none", "switch", "refollow"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			reader := metadataFunc(func(ctx context.Context, k, id string) (bilibili.Metadata, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-release:
				case <-ctx.Done():
					return bilibili.Metadata{}, ctx.Err()
				}
				return bilibili.Metadata{Kind: k, ID: id, Name: "live", ObservedAt: time.Now().UnixMilli()}, nil
			})
			s, _ := New(&detailMemory{}, remote.New(nil), reader)
			_, _ = s.Add("creator", "123")
			var wg sync.WaitGroup
			results := make(chan error, 8)
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.Current(context.Background(), "creator", "123", false)
				results <- err
			}()
			<-started
			if change == "switch" {
				_ = s.Configure("http://127.0.0.1:8181")
			}
			if change == "refollow" {
				_ = s.Remove("creator", "123")
				_, _ = s.Add("creator", "123")
			}
			if change == "none" {
				for i := 0; i < 7; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := s.Current(context.Background(), "creator", "123", false)
						results <- err
					}()
				}
			}
			close(release)
			wg.Wait()
			close(results)
			for err := range results {
				if (err == nil) != (change == "none") {
					t.Fatal(change, err)
				}
			}
			v, _ := s.CurrentCache("creator", "123")
			if v.Available != (change == "none") || calls.Load() != 1 {
				t.Fatal(v, calls.Load())
			}
		})
	}
}

func TestHistoryRangeIsolationAndNoReconcile(t *testing.T) {
	var fail atomic.Bool
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" || r.URL.Path != "/api/detail/creator/123" {
			t.Error("history queried other resource", r.Method, r.URL.Path)
		}
		if fail.Load() {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"offline"}`))
			return
		}
		w.Write([]byte(`{"record":{"kind":"creator","id":"123","seq":7,"recorded":true,"observed_at":100},"target":{"kind":"creator","id":"123","recorded":true,"tracking_state":"paused","tracking":false,"status":"paused"},"metrics":{"points":[{"at":0,"value":0}],"truncated":false}}`))
	}))
	defer server.Close()
	reader := metadataFunc(func(context.Context, string, string) (bilibili.Metadata, error) {
		t.Error("history called platform")
		return bilibili.Metadata{}, errors.New("unexpected")
	})
	s, _ := New(&detailMemory{}, remote.New(nil), reader)
	_ = s.Configure(server.URL)
	_, _ = s.Add("creator", "123")
	v, err := s.History(context.Background(), "creator", "123", "30")
	if err != nil || v.Target.TrackingState != "paused" || len(v.Metrics.Points) != 1 {
		t.Fatal(v, err)
	}
	fail.Store(true)
	v, err = s.History(context.Background(), "creator", "123", "30")
	if err != nil || !v.Cached || v.ReadError == "" {
		t.Fatal(v, err)
	}
	if _, err = s.History(context.Background(), "creator", "123", "90"); err == nil {
		t.Fatal("wrong range cache reused")
	}
	if count.Load() != 3 {
		t.Fatal("unexpected additional requests", count.Load())
	}
	_ = s.Configure("http://127.0.0.1:8181")
	if len(s.state.History) != 0 {
		t.Fatal("history cache survived switch")
	}
}

func TestLegacyDetailCacheSurvivesUpgrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"offline"}`))
	}))
	defer server.Close()
	reader := metadataFunc(func(context.Context, string, string) (bilibili.Metadata, error) {
		return bilibili.Metadata{}, errors.New("platform unavailable")
	})
	s, _ := New(&detailMemory{}, remote.New(nil), reader)
	_ = s.Configure(server.URL)
	_, _ = s.Add("creator", "123")
	d := remote.Detail{Item: remote.Item{Kind: "creator", ID: "123", Name: "legacy", Seq: 3, ObservedAt: 100, Recorded: true}}
	d.Metrics.Points = []remote.Point{{At: 0, Value: 99}}
	s.state.Follows[0].Data = &d
	s.state.Follows[0].ServerURL = server.URL
	v, err := s.Current(context.Background(), "creator", "123", true)
	if err != nil || !v.Available || !v.Cached || v.Source != "server" || v.Item.Name != "legacy" || v.Item.Seq != 3 {
		t.Fatal(v, err)
	}
	h, err := s.History(context.Background(), "creator", "123", "30")
	if err != nil || !h.Cached || len(h.Metrics.Points) != 1 || h.Record.Seq != 3 {
		t.Fatal(h, err)
	}
	if _, err = s.History(context.Background(), "creator", "123", "7"); err == nil {
		t.Fatal("legacy default cache used for another range")
	}
}

func TestCurrentCancellationAndPersistenceFailure(t *testing.T) {
	reader := metadataFunc(func(ctx context.Context, k, id string) (bilibili.Metadata, error) {
		if err := ctx.Err(); err != nil {
			return bilibili.Metadata{}, err
		}
		return bilibili.Metadata{Kind: k, ID: id, Name: "live", ObservedAt: time.Now().UnixMilli()}, nil
	})
	s, _ := New(&failingStore{fail: true}, remote.New(nil), reader)
	v, err := s.Current(context.Background(), "creator", "123", false)
	if err != nil || !v.Available || v.CacheError == "" || len(s.state.Current) != 0 {
		t.Fatal(v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Current(ctx, "creator", "123", true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was converted to fallback", err)
	}
}

func TestCreatorVideoCatalogUsesCurrentBeforeScanFallback(t *testing.T) {
	reader := metadataFunc(func(_ context.Context, k, id string) (bilibili.Metadata, error) {
		n := int64(200)
		return bilibili.Metadata{Kind: k, ID: id, Name: "live title", OwnerID: "123", Metric: &n, ObservedAt: time.Now().UnixMilli()}, nil
	})
	s, _ := New(&detailMemory{}, remote.New(nil), reader)
	_, _ = s.Add("video", "BV17x411w7KC")
	if _, err := s.Current(context.Background(), "video", "BV17x411w7KC", false); err != nil {
		t.Fatal(err)
	}
	old := int64(10)
	page := s.ListCatalog("video", "live", 1, false, "views", "123", []bilibili.Video{{ID: "BV17x411w7KC", Title: "old scan title", Views: &old}})
	if page.Total != 1 || page.Items[0].Current == nil || followItem(page.Items[0]).Name != "live title" || *followItem(page.Items[0]).Metric != 200 {
		t.Fatal("scan data masked current profile", page)
	}
}
