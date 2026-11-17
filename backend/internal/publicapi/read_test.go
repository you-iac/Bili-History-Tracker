// read_test.go 验证8181公共 JSON、精确状态及兼容单目标应答，读取不调用上游。
package publicapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
