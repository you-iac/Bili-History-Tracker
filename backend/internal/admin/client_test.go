// client_test.go 验证8182同路径 JSON、精确状态及客户端提交，不通过只读请求采集。
package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientJSONReadsAndExactStatus(t *testing.T) {
	s, store, q := dashboardServer(t)
	seedDashboard(t, store)
	for _, path := range []string{"/videos", "/creators", "/detail/video/BV17x411w7KC", "/detail/video/BV17x411w7KC?seq=1", "/detail/creator/12345"} {
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"existing"`) || q.calls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/targets", strings.NewReader(`{"type":"video","id":"BV1oa41137pj"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatal("unverified target accepted", w.Code, w.Body.String())
	}
}
