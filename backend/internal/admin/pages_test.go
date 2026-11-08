// pages_test.go 验证真实数据页面、趋势 API、只读浏览、输入边界和历史保留。
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
	"bili-history-tracker/backend/internal/tracker"
)

type guardedQuerier struct{ calls atomic.Int64 }

func (q *guardedQuerier) GetVideo(context.Context, string, string) (source.Video, error) {
	q.calls.Add(1)
	return source.Video{}, &source.Error{Kind: source.UpstreamFailure}
}
func (q *guardedQuerier) GetCreator(context.Context, string, string) (source.CreatorProfile, error) {
	q.calls.Add(1)
	return source.CreatorProfile{}, &source.Error{Kind: source.UpstreamFailure}
}
func (q *guardedQuerier) ListCreatorVideos(context.Context, string, string) ([]source.CreatorVideo, error) {
	q.calls.Add(1)
	return nil, nil
}

func dashboardServer(t *testing.T) (*Server, *storage.Store, *guardedQuerier) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	log, err := tracker.NewLog("", 500)
	if err != nil {
		t.Fatal(err)
	}
	q := &guardedQuerier{}
	trk := tracker.New(store, q, log, "bilibili")
	server, err := New(store, trk, log, "bilibili")
	if err != nil {
		t.Fatal(err)
	}
	return server, store, q
}

func seedDashboard(t *testing.T, store *storage.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	count := int64(100)
	profile := source.CreatorProfile{Platform: "bilibili", ID: "12345", Name: "观测 UP", FollowerCount: &count, ObservedAt: now.AddDate(0, 0, -10), Raw: json.RawMessage(`{"card":{"mid":"12345","name":"观测 UP","sign":"签名"},"follower":100}`)}
	cfp, err := storage.CreatorFingerprint(profile.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveCreator(ctx, profile, cfp); err != nil {
		t.Fatal(err)
	}
	for seq, title := range []string{"旧标题", `<script>alert("x")</script>`} {
		raw, _ := json.Marshal(map[string]any{"bvid": "BV17x411w7KC", "title": title, "desc": "说明", "owner": map[string]any{"mid": 12345}, "pages": []any{}})
		v := source.Video{Platform: "bilibili", ID: "BV17x411w7KC", Title: title, Creator: source.Creator{ID: "12345", Name: "观测 UP"}, ViewCount: &count, ObservedAt: now.Add(time.Duration(seq-1) * time.Hour), Raw: raw}
		fp, e := storage.VideoFingerprint(raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.SaveVideo(ctx, v, fp); e != nil {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{"video", "creator"} {
		id := "BV17x411w7KC"
		metric := "view"
		if kind == "creator" {
			id = "12345"
			metric = "follower"
		}
		if err = store.AddTarget(ctx, "bilibili", kind, id, time.Hour, now); err != nil {
			t.Fatal(err)
		}
		if err = store.RecordTargetResult(ctx, storage.Target{Platform: "bilibili", TargetType: kind, TargetID: id}, "ok", now); err != nil {
			t.Fatal(err)
		}
		for _, offset := range []int{-3, 0} {
			if err = store.RecordMetric(ctx, "bilibili", kind, id, metric, now.Truncate(24*time.Hour).AddDate(0, 0, offset), int64(100-offset)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDashboardPagesAndAPI(t *testing.T) {
	server, store, q := dashboardServer(t)
	seedDashboard(t, store)
	for _, route := range []string{"/", "/videos", "/videos?sort=growth", "/creators", "/creators?following=yes", "/manage", "/activity", "/activity?tab=issues", "/activity?tab=recovered", "/logs", "/detail/video/BV17x411w7KC", "/detail/video/BV17x411w7KC?seq=1", "/detail/creator/12345", "/assets/app.css", "/assets/app.js"} {
		t.Run(route, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", route, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), `<script>alert("x")</script>`) {
				t.Fatal("upstream title was not escaped")
			}
		})
	}
	for _, route := range []string{"/api/metrics/video/BV17x411w7KC?days=7", "/api/metrics/creator/12345?days=all"} {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		var series storage.MetricSeries
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &series) != nil || len(series.Points) != 2 {
			t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
		}
		if series.Points[1].Value >= series.Points[0].Value {
			t.Fatal("counter decrease was discarded")
		}
	}
	for route, status := range map[string]int{"/does-not-exist": 404, "/detail/wrong/12345": 404, "/detail/video/BV17x411w7KC?seq=999": 400, "/api/metrics/video/BV17x411w7KC?days=invalid": 400} {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		if rec.Code != status {
			t.Fatalf("%s status=%d want=%d", route, rec.Code, status)
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("read-only browsing triggered upstream requests")
	}
}

func TestEmptyDashboardPages(t *testing.T) {
	server, _, _ := dashboardServer(t)
	for _, route := range []string{"/", "/videos", "/creators", "/manage", "/activity", "/logs"} {
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		if rec.Code != 200 {
			t.Fatalf("empty %s: %d %s", route, rec.Code, rec.Body.String())
		}
	}
}

func TestNormalizeInputAndReturnURL(t *testing.T) {
	for _, tc := range []struct{ kind, input, want string }{{"video", " https://www.bilibili.com/video/BV17x411w7KC/?p=2 ", "BV17x411w7KC"}, {"creator", "https://space.bilibili.com/12345/", "12345"}, {"video", "https://evil.test/video/BV17x411w7KC", ""}, {"video", "https://www.bilibili.com.evil.test/video/BV17x411w7KC", ""}, {"video", "https://www.bilibili.com:9000/video/BV17x411w7KC", ""}, {"creator", "https://space.bilibili.com/12345/extra", ""}} {
		if got := normalizeID(tc.kind, tc.input); got != tc.want {
			t.Fatalf("normalize %s = %s", tc.input, got)
		}
	}
	for _, value := range []string{"https://evil.test", "//evil.test", "javascript:alert(1)", "/arbitrary"} {
		if got := safeReturn(value); got != "/manage" {
			t.Fatalf("unsafe redirect %s", got)
		}
	}
	if safeReturn("/videos?sort=popular") != "/videos?sort=popular" {
		t.Fatal("safe filter lost")
	}
}

func TestStopFollowingPreservesHistory(t *testing.T) {
	server, store, _ := dashboardServer(t)
	seedDashboard(t, store)
	form := url.Values{"type": {"video"}, "id": {"BV17x411w7KC"}, "return_to": {"https://evil.test"}}
	request := httptest.NewRequest(http.MethodPost, "/targets/delete", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, request)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/manage?") {
		t.Fatalf("stop: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	detail, err := store.LatestVideo(context.Background(), "bilibili", "BV17x411w7KC")
	if err != nil || detail.CurrentSeq != 2 {
		t.Fatal("stopping removed content history")
	}
}

func TestContentDiffIgnoresCountersAndPageOrder(t *testing.T) {
	before := []byte(`{"title":"A","stat":{"view":1},"pages":[{"page":2,"cid":2,"part":"P2","duration":1},{"page":1,"cid":1,"part":"P1","duration":1}]}`)
	after := []byte(`{"title":"A","stat":{"view":99},"pages":[{"page":1,"cid":1,"part":"P1","duration":1},{"page":2,"cid":2,"part":"P2","duration":1}]}`)
	if changes := contentChanges("video", before, after); len(changes) != 0 {
		t.Fatalf("false content diff: %+v", changes)
	}
	after = []byte(`{"title":"B","pages":[{"page":1,"cid":1,"part":"P1","duration":1},{"page":2,"cid":2,"part":"P2","duration":1}]}`)
	if changes := contentChanges("video", before, after); len(changes) != 1 || changes[0].Name != "标题" {
		t.Fatalf("title diff: %+v", changes)
	}
}
