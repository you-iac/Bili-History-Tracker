// current_test.go 验证真实本地会话入口贯通资料刷新、整页历史和UP已关注视频，不转发会话。
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/client/agent/internal/bilibili"
	"bili-history-tracker/client/agent/internal/library"
	"bili-history-tracker/client/agent/internal/remote"
)

type currentPlatform struct{}

func (currentPlatform) Current(_ context.Context, kind, id string) (bilibili.Metadata, error) {
	value := int64(100)
	return bilibili.Metadata{Kind: kind, ID: id, Name: "本地视频", Cover: "https://i0.hdslb.com/current.jpg", Metric: &value, OwnerID: "123", At: time.Now().UnixMilli()}, nil
}

func TestCurrentPageHTTPRefreshAndCreatorFollowedVideos(t *testing.T) {
	h := setup(t)
	var batches atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics/batch" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected remote request", r.URL, r.Header)
			http.Error(w, "unexpected", 500)
			return
		}
		batches.Add(1)
		var input struct {
			Kind string
			IDs  []string
		}
		json.NewDecoder(r.Body).Decode(&input)
		items := []remote.BatchMetric{}
		for _, id := range input.IDs {
			items = append(items, remote.BatchMetric{Kind: input.Kind, ID: id, Points: []remote.Point{}})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer upstream.Close()
	collection, err := library.New(h.s.store, remote.New(nil), currentPlatform{})
	if err != nil {
		t.Fatal(err)
	}
	h.s.library = collection
	collection.Configure(upstream.URL)
	collection.Add("video", "BV17x411w7KC")
	collection.Add("video", "BV1oa41137pj")
	w := h.call("POST", "/api/follows/refresh?kind=video&id=BV17x411w7KC&id=BV1oa41137pj", "{}")
	if w.Code != 200 || batches.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), batches.Load())
	}
	w = h.call("GET", "/api/creator-videos/123?scope=followed", "")
	var reply struct {
		Catalog struct {
			Total int
			Items []struct{ Summary *remote.Item }
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Catalog.Total != 2 || reply.Catalog.Items[0].Summary == nil || reply.Catalog.Items[0].Summary.Name != "本地视频" || batches.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
