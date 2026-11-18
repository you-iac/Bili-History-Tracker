// read_test.go 验证8181公共 JSON、精确状态及兼容单目标应答，读取不调用上游。
package publicapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili-history-tracker/backend/internal/source"
	"bili-history-tracker/backend/internal/storage"
)

// 作者引用行必须保留给视频关联，但不能作为完整作者资料供详情或指标成功读取。
func TestCreatorPlaceholderReadsPreserveTargetState(t *testing.T) {
	for _, state := range []string{"absent", "active", "paused"} {
		t.Run(state, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			ctx := context.Background()
			video := source.Video{Platform: "bilibili", ID: "BV17x411w7KC", Title: "引用视频", Creator: source.Creator{ID: "987", Name: "占位 UP"}, ObservedAt: time.Now().UTC(), Raw: json.RawMessage(`{"bvid":"BV17x411w7KC","title":"引用视频","owner":{"mid":987,"name":"占位 UP"}}`)}
			fp, err := storage.VideoFingerprint(video.Raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SaveVideo(ctx, video, fp); err != nil {
				t.Fatal(err)
			}
			if state != "absent" {
				if err = store.AddTarget(ctx, "bilibili", "creator", "987", time.Hour, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if state == "paused" {
				if _, err = store.DB().Exec("UPDATE tracking_targets SET enabled=0 WHERE target_id='987'"); err != nil {
					t.Fatal(err)
				}
			}
			// Store 只使用一个连接；统计写入数可验证读取未新增任务、版本或指标。
			var before, after int64
			if err = store.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			h := s.Handler()
			paths := []string{"/detail/creator/987"}
			for _, days := range []string{"7", "30", "90", "all"} {
				paths = append(paths, "/api/metrics/creator/987?days="+days)
			}
			for _, path := range paths {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				var reply struct {
					OK     bool         `json:"ok"`
					Code   string       `json:"code"`
					Target *targetState `json:"target"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.OK || reply.Code != "DETAIL_NOT_RECORDED" || reply.Target == nil {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
				}
				target := reply.Target
				if target.Kind != "creator" || target.ID != "987" || target.Recorded || target.TrackingState != state || target.Tracking != (state == "active") {
					t.Fatalf("%s: %s", path, w.Body.String())
				}
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/api/targets/status?kind=creator&id=987", nil))
				var status targetState
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status != *target {
					t.Fatalf("status differs from %s: %s", path, w.Body.String())
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/creators?id=987", nil))
			var catalog struct {
				Items []struct {
					ID       string `json:"id"`
					Seq      int64  `json:"seq"`
					Recorded bool   `json:"recorded"`
				} `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Items) != 1 || catalog.Items[0].ID != "987" || catalog.Items[0].Seq != 0 || catalog.Items[0].Recorded {
				t.Fatal("placeholder reference must remain visible without a recorded profile", w.Body.String())
			}
			if err = store.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after || q.calls.Load() != 0 {
				t.Fatal("reads wrote to storage or queried upstream", before, after, q.calls.Load())
			}
		})
	}
}

func TestRecordedCreatorMetricsAllowEmptyAndZeroSamples(t *testing.T) {
	for _, sample := range []string{"none", "outside", "zero"} {
		t.Run(sample, func(t *testing.T) {
			s, store, q := dashboardServer(t)
			ctx := context.Background()
			now := time.Now().UTC()
			profile := source.CreatorProfile{Platform: "bilibili", ID: "987", Name: "完整 UP", ObservedAt: now, Raw: json.RawMessage(`{"card":{"mid":"987","name":"完整 UP"}}`)}
			fp, err := storage.CreatorFingerprint(profile.Raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.SaveCreator(ctx, profile, fp); err != nil {
				t.Fatal(err)
			}
			at, value := now.Truncate(24*time.Hour), int64(0)
			if sample == "outside" {
				at, value = at.AddDate(0, 0, -100), 42
			}
			if sample != "none" {
				if err = store.RecordMetric(ctx, "bilibili", "creator", "987", "follower", at, value); err != nil {
					t.Fatal(err)
				}
			}
			h := s.Handler()
			for _, days := range []string{"7", "30", "90", "all"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/api/metrics/creator/987?days="+days, nil))
				var reply struct {
					OK   bool   `json:"ok"`
					Code string `json:"code"`
					storage.MetricSeries
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || !reply.OK || reply.Code != "OK" || reply.Points == nil || reply.Truncated {
					t.Fatalf("%s: %d %s", days, w.Code, w.Body.String())
				}
				want := 0
				if sample == "zero" || sample == "outside" && days == "all" {
					want = 1
				}
				if len(reply.Points) != want || want == 1 && (reply.Points[0].Value != value || reply.Points[0].At != at.UnixMilli()) {
					t.Fatalf("%s: incorrect samples: %s", days, w.Body.String())
				}
				// 详情同样允许完整资料没有粉丝指标，且不依赖当前是否有跟踪任务。
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/detail/creator/987?days="+days, nil))
				var detail struct {
					OK      bool                    `json:"ok"`
					Target  *targetState            `json:"target"`
					Item    struct{ Recorded bool } `json:"item"`
					Metrics storage.MetricSeries    `json:"metrics"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || !detail.OK || !detail.Item.Recorded || detail.Target == nil || !detail.Target.Recorded || detail.Target.Tracking || detail.Target.TrackingState != "absent" || len(detail.Metrics.Points) != want {
					t.Fatal("recorded profile treated as missing", w.Body.String())
				}
			}
			if q.calls.Load() != 0 {
				t.Fatal("reads queried upstream")
			}
		})
	}
}

func TestClientOverviewPublicContract(t *testing.T) {
	for _, populated := range []bool{false, true} {
		s, store, q := dashboardServer(t)
		if populated {
			seedDashboard(t, store)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var data struct {
			Summary   map[string]int64  `json:"summary"`
			Creators  []json.RawMessage `json:"creators"`
			Videos    []json.RawMessage `json:"videos"`
			Updates   []json.RawMessage `json:"updates"`
			FetchedAt time.Time         `json:"fetched_at"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.FetchedAt.IsZero() || data.Creators == nil || data.Videos == nil || data.Updates == nil {
			t.Fatalf("invalid overview: %d %s", w.Code, w.Body.String())
		}
		want := int64(0)
		if populated {
			want = 1
		}
		if data.Summary["creators"] != want || data.Summary["videos"] != want || data.Summary["tracking"] != 2*want || len(data.Creators) != int(want) || len(data.Videos) != int(want) {
			t.Fatal("overview does not match public records", w.Body.String())
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &fields)
		if len(fields) != 7 || q.calls.Load() != 0 {
			t.Fatal("overview exposed extra fields or queried upstream", w.Body.String())
		}
	}
}

func TestClientJSONReadsAndExactStatus(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	for _, path := range []string{"/", "/videos", "/creators", "/creators?q=12345", "/detail/video/BV17x411w7KC", "/detail/video/BV17x411w7KC?seq=1", "/detail/creator/12345"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		path               string
		recorded, tracking bool
	}{
		{"/api/targets/status?kind=video&id=BV17x411w7KC", true, true},
		{"/api/targets/status?kind=creator&id=99999", false, false},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		var state struct {
			Recorded bool `json:"recorded"`
			Tracking bool `json:"tracking"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.Recorded != tc.recorded || state.Tracking != tc.tracking {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if q.calls.Load() != 0 {
		t.Fatal("reads queried upstream")
	}
	r := httptest.NewRequest("POST", "/targets", strings.NewReader(`{"type":"video","id":"BV17x411w7KC"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"result":"existing"`) || q.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/targets", strings.NewReader(`{"type":"video","id":"BV1oa41137pj"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"TARGET_SUBMISSION_FAILED"`) {
		t.Fatal("unverified target accepted", w.Code, w.Body.String())
	}
}
