// discovery_test.go 验证统一列表 API、显式导入与状态轮询的 HTTP 闭环。
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/discovery"
	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

type importQueries struct{ calls atomic.Int64 }

func (q *importQueries) GetCreator(context.Context, string, string) (source.CreatorProfile, error) {
	return source.CreatorProfile{}, nil
}
func (q *importQueries) GetCreatorVideoPage(_ context.Context, _, _, cursor string) (source.CreatorVideoPage, error) {
	q.calls.Add(1)
	if cursor == "" {
		return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "BV1oa41137pj", Title: "A"}}, NextCursor: "two"}, nil
	}
	return source.CreatorVideoPage{Items: []source.CreatorVideo{{ID: "BV1Rt4y1j7uP", Title: "B"}}}, nil
}
func (q *importQueries) GetVideo(_ context.Context, platform, id string) (source.Video, error) {
	q.calls.Add(1)
	return source.Video{Platform: platform, ID: id, Title: id, Creator: source.Creator{ID: "1", Name: "UP"}, ObservedAt: time.Now(), Raw: []byte(fmt.Sprintf(`{"bvid":%q,"title":"A","owner":{"mid":1,"name":"UP"}}`, id))}, nil
}
func TestCreatorVideoHTTPWorkflow(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "discovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log, err := tracker.NewLog("", 30)
	if err != nil {
		t.Fatal(err)
	}
	q := &importQueries{}
	server, err := New(store, tracker.New(store, q, log, "bilibili"), log, "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	handler := server.Handler()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := get("/api/creators/1/videos?scope=recorded")
	if w.Code != 200 || q.calls.Load() != 0 {
		t.Fatalf("recorded view queried upstream: %d", w.Code)
	}
	w = get("/api/creators/1/videos?scope=available&limit=1")
	var page discovery.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Session == "" {
		t.Fatalf("first page: %d %s %v", w.Code, w.Body, err)
	}
	if counts, err := store.Counts(context.Background()); err != nil || counts["videos"] != 0 {
		t.Fatalf("browsing persisted content: %v %v", counts, err)
	}
	body := fmt.Sprintf(`{"platform":"bilibili","creator_id":"1","all":true,"session":%q}`, page.Session)
	post := httptest.NewRequest("POST", "/api/ingest/videos", strings.NewReader(body))
	post.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, post)
	var job discovery.Progress
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || w.Code != 202 {
		t.Fatalf("start: %d %s %v", w.Code, w.Body, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w = get("/api/ingest/jobs/" + job.ID)
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if job.State != "complete" || job.Added != 2 {
		t.Fatalf("task: %+v", job)
	}
	w = get("/api/creators/1/videos?scope=recorded")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 2 || !page.Items[0].Subscribed {
		t.Fatalf("recorded history: %s %v", w.Body, err)
	}
	for _, path := range []string{"/api/creators/1/videos?scope=no", "/api/creators/1/videos?scope=available&limit=0", "/api/creators/1/videos?platform=unknown"} {
		if w = get(path); w.Code != 400 {
			t.Fatalf("invalid input accepted: %s %d", path, w.Code)
		}
	}
	post = httptest.NewRequest("POST", "/api/ingest/videos", strings.NewReader(body))
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Origin", "https://another.example")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, post)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin write accepted: %d", w.Code)
	}
}
